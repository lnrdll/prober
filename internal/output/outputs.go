package output

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/lnrdll/prober/internal/prober"
)

// Setting is an output's flag value and whether the user set it.
type Setting struct {
	Value string
	Set   bool
}

// Config is everything Open needs to start the selected outputs.
type Config struct {
	// Selections holds the raw -o values.
	Selections []string
	// Writer receives stdout JSON lines and the summary. Nil means os.Stdout.
	Writer  io.Writer
	File    Setting
	JUnit   Setting
	Datadog Setting
	GCP     Setting
}

type publisher interface {
	Publish(res prober.Result) error
	Close() error
}

type summaryPublisher interface {
	PublishSummary(summary prober.Summary) error
}

type constructorFunc func(cfg Config) (publisher, error)

// defaultConstructors builds each adapter from a Config whose settings already
// hold their defaults.
var defaultConstructors = map[Name]constructorFunc{
	OutputSummary:       newSummaryOutput,
	OutputJunit:         newJUnitOutput,
	OutputStdout:        newStdoutPublisher,
	OutputFile:          newFilePublisher,
	OutputStatsdDatadog: newDatadogPublisher,
	OutputStatsdGCP:     newGCPPublisher,
}

// Outputs publishes results to every started adapter and keeps their failures
// until Finish reports them.
type Outputs struct {
	adapters []namedPublisher
	failures failures
}

type namedPublisher struct {
	name Name
	pub  publisher
}

// Open validates the selection, applies defaults and starts every selected
// output, or stdout alone when nothing is selected. If an output fails to
// start, Open closes the ones it already started and reports both.
func Open(cfg Config) (*Outputs, error) {
	return openWith(cfg, defaultConstructors)
}

func openWith(cfg Config, builders map[Name]constructorFunc) (*Outputs, error) {
	selected, err := parseSelections(cfg.Selections)
	if err != nil {
		return nil, err
	}
	if cfg, err = applySettings(cfg, selected); err != nil {
		return nil, err
	}
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if len(selected) == 0 {
		selected = []Name{OutputStdout}
	}

	o := &Outputs{failures: newFailures(selected)}
	for _, name := range selected {
		pub, err := builders[name](cfg)
		if err != nil {
			o.failures.record(name, opInit, err)
			o.closeAll()
			return nil, o.failures.report()
		}
		o.adapters = append(o.adapters, namedPublisher{name: name, pub: pub})
	}
	return o, nil
}

// Publish sends res to every output and records each failure.
func (o *Outputs) Publish(res prober.Result) {
	for _, a := range o.adapters {
		if err := a.pub.Publish(res); err != nil {
			o.failures.record(a.name, opPublish, err)
		}
	}
}

// Finish publishes the summary to every output that takes one, closes every
// output and returns the grouped failure report, or nil.
func (o *Outputs) Finish(summary prober.Summary) error {
	for _, a := range o.adapters {
		if sp, ok := a.pub.(summaryPublisher); ok {
			if err := sp.PublishSummary(summary); err != nil {
				o.failures.record(a.name, opPublishSummary, err)
			}
		}
	}
	o.closeAll()
	return o.failures.report()
}

func (o *Outputs) closeAll() {
	for _, a := range o.adapters {
		if err := a.pub.Close(); err != nil {
			o.failures.record(a.name, opClose, err)
		}
	}
	o.adapters = nil
}

// UnselectedSettingError reports that a setting was provided for an output that
// was not selected.
type UnselectedSettingError struct {
	Output Name
}

func (e UnselectedSettingError) Error() string {
	return fmt.Sprintf("setting for output %q requires selecting %q", e.Output, e.Output)
}

// EmptySettingError reports that a selected output received an explicitly empty
// setting value.
type EmptySettingError struct {
	Output Name
}

func (e EmptySettingError) Error() string {
	return fmt.Sprintf("setting for output %q cannot be empty", e.Output)
}

// applySettings checks each output's setting against the selection and fills
// in the default for a selected output whose setting is unset and empty.
func applySettings(cfg Config, selected []Name) (Config, error) {
	settings := []struct {
		name     Name
		setting  *Setting
		fallback string
	}{
		{OutputJunit, &cfg.JUnit, DefaultJUnitPath},
		{OutputFile, &cfg.File, DefaultFilePath},
		{OutputStatsdDatadog, &cfg.Datadog, DefaultStatsDAddr},
		{OutputStatsdGCP, &cfg.GCP, DefaultStatsDAddr},
	}
	for _, s := range settings {
		isSelected := slices.Contains(selected, s.name)
		switch {
		case !isSelected && s.setting.Set:
			return Config{}, UnselectedSettingError{Output: s.name}
		case isSelected && s.setting.Value == "" && !s.setting.Set:
			s.setting.Value = s.fallback
		case isSelected && s.setting.Value == "":
			return Config{}, EmptySettingError{Output: s.name}
		}
	}
	return cfg, nil
}

type operation int

const (
	opInit operation = iota
	opPublish
	opPublishSummary
	opClose
	operationCount
)

func (op operation) String() string {
	return [...]string{"init", "publish", "publish summary", "close"}[op]
}

type failureKey struct {
	name Name
	op   operation
}

type failure struct {
	count int
	first error
}

// failures counts errors per output and operation and keeps the first one.
type failures struct {
	order []Name
	byKey map[failureKey]*failure
}

func newFailures(order []Name) failures {
	return failures{order: order, byKey: make(map[failureKey]*failure)}
}

func (f *failures) record(name Name, op operation, err error) {
	key := failureKey{name: name, op: op}
	if entry, ok := f.byKey[key]; ok {
		entry.count++
		return
	}
	f.byKey[key] = &failure{count: 1, first: err}
}

// report returns one error with a line per failing output and operation, in
// selection order and then operation order, or nil when nothing failed.
func (f *failures) report() error {
	if len(f.byKey) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("output errors:")
	for _, name := range f.order {
		for op := opInit; op < operationCount; op++ {
			entry, ok := f.byKey[failureKey{name: name, op: op}]
			if !ok {
				continue
			}
			if entry.count > 1 {
				fmt.Fprintf(&b, "\n  %s: %s failed %d times: %v", name, op, entry.count, entry.first)
			} else {
				fmt.Fprintf(&b, "\n  %s: %s failed: %v", name, op, entry.first)
			}
		}
	}
	return errors.New(b.String())
}
