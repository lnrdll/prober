package output

import (
	"fmt"
	"slices"
	"strings"
)

type Name string

const (
	DefaultFilePath   = "prober.log"
	DefaultJUnitPath  = "prober.xml"
	DefaultStatsDAddr = "127.0.0.1:8125"

	OutputSummary       Name = "summary"
	OutputJunit         Name = "junit"
	OutputStdout        Name = "stdout"
	OutputFile          Name = "file"
	OutputStatsdDatadog Name = "statsd-datadog"
	OutputStatsdGCP     Name = "statsd-gcp"
)

var orderedNames = []Name{
	OutputSummary,
	OutputJunit,
	OutputStdout,
	OutputFile,
	OutputStatsdDatadog,
	OutputStatsdGCP,
}

func Names() []Name {
	return append([]Name(nil), orderedNames...)
}

func parseSelections(raw []string) ([]Name, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	selected := make([]Name, 0, len(raw))
	seen := make(map[Name]struct{}, len(raw))
	for _, value := range raw {
		name := Name(strings.TrimSpace(value))
		if !slices.Contains(orderedNames, name) {
			return nil, fmt.Errorf("invalid output %q (allowed: %s)", value, JoinNames(orderedNames))
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		selected = append(selected, name)
	}

	return selected, nil
}

func JoinNames(names []Name) string {
	values := make([]string, 0, len(names))
	for _, name := range names {
		values = append(values, string(name))
	}

	return strings.Join(values, ", ")
}
