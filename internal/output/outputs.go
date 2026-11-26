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
	Writer io.Writer
	// Settings holds optional per-output settings keyed by output name.
	Settings map[Name]Setting
}

type publisher interface {
	Publish(res prober.Result) error
	Close() error
}

type summaryPublisher interface {
	PublishSummary(summary prober.Summary) error
}

type constructorFunc func(cfg Config) (publisher, error)

type settingSpec struct {
	defaultValue string
	description  string
}

type catalogEntry struct {
	name      Name
	construct constructorFunc
	setting   *settingSpec
}

// catalog is the single ordered registry for all built-in outputs.
var catalog = []catalogEntry{
	{
		name:      outputSummary,
		construct: newSummaryOutput,
	},
	{
		name:      outputJunit,
		construct: newJUnitOutput,
		setting: &settingSpec{
			defaultValue: defaultJUnitPath,
			description:  "Write a JUnit XML report to a file",
		},
	},
	{
		name:      outputStdout,
		construct: newStdoutPublisher,
	},
	{
		name:      outputFile,
		construct: newFilePublisher,
		setting: &settingSpec{
			defaultValue: defaultFilePath,
			description:  "Append structured JSON log lines to a file",
		},
	},
	{
		name:      outputStatsdDatadog,
		construct: newDatadogPublisher,
		setting: &settingSpec{
			defaultValue: defaultStatsDAddr,
			description:  "UDP network address location pointing to local Datadog daemon",
		},
	},
	{
		name:      outputStatsdGCP,
		construct: newGCPPublisher,
		setting: &settingSpec{
			defaultValue: defaultStatsDAddr,
			description:  "UDP network address location pointing to local GCP Ops Agent",
		},
	},
}

func findCatalogEntry(name Name) (catalogEntry, bool) {
	for _, entry := range catalog {
		if entry.name == name {
			return entry, true
		}
	}
	return catalogEntry{}, false
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
	return openWith(cfg, nil)
}

func openWith(cfg Config, overrides map[Name]constructorFunc) (*Outputs, error) {
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
		selected = []Name{outputStdout}
	}

	o := &Outputs{failures: newFailures(selected)}
	for _, name := range selected {
		construct := overrides[name]
		if construct == nil {
			entry, _ := findCatalogEntry(name)
			construct = entry.construct
		}
		pub, err := construct(cfg)
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

// applySettings validates Config.Settings without mutating the caller's map,
// checks each configurable output's setting against the selection, and fills
// in the default for a selected output whose setting is unset and empty.
func applySettings(cfg Config, selected []Name) (Config, error) {
	var invalid []Name
	for name := range cfg.Settings {
		entry, ok := findCatalogEntry(name)
		if !ok || entry.setting == nil {
			invalid = append(invalid, name)
		}
	}
	if len(invalid) > 0 {
		slices.Sort(invalid)
		name := invalid[0]
		if _, ok := findCatalogEntry(name); !ok {
			return Config{}, invalidOutputError(string(name))
		}
		return Config{}, fmt.Errorf("output %q takes no setting", name)
	}

	resolved := make(map[Name]Setting, len(cfg.Settings))
	for _, entry := range catalog {
		if entry.setting == nil {
			continue
		}
		s := cfg.Settings[entry.name]
		isSelected := slices.Contains(selected, entry.name)
		switch {
		case !isSelected && s.Set:
			return Config{}, UnselectedSettingError{Output: entry.name}
		case isSelected && s.Value == "" && !s.Set:
			s.Value = entry.setting.defaultValue
		case isSelected && s.Value == "":
			return Config{}, EmptySettingError{Output: entry.name}
		}
		resolved[entry.name] = s
	}
	cfg.Settings = resolved
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
