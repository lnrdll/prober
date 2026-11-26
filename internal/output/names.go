package output

import (
	"fmt"
	"strings"
)

type Name string

// SettingDescriptor describes a configurable output's setting metadata.
type SettingDescriptor struct {
	Name        Name
	Default     string
	Description string
}

const (
	defaultFilePath   = "prober.log"
	defaultJUnitPath  = "prober.xml"
	defaultStatsDAddr = "127.0.0.1:8125"

	outputSummary       Name = "summary"
	outputJunit         Name = "junit"
	outputStdout        Name = "stdout"
	outputFile          Name = "file"
	outputStatsdDatadog Name = "statsd-datadog"
	outputStatsdGCP     Name = "statsd-gcp"
)

func Names() []Name {
	names := make([]Name, 0, len(catalog))
	for _, entry := range catalog {
		names = append(names, entry.name)
	}
	return names
}

func SettingDescriptors() []SettingDescriptor {
	descriptors := make([]SettingDescriptor, 0, len(catalog))
	for _, entry := range catalog {
		if entry.setting == nil {
			continue
		}
		descriptors = append(descriptors, SettingDescriptor{
			Name:        entry.name,
			Default:     entry.setting.defaultValue,
			Description: entry.setting.description,
		})
	}
	return descriptors
}

func invalidOutputError(value string) error {
	return fmt.Errorf("invalid output %q (allowed: %s)", value, JoinNames(Names()))
}

func parseSelections(raw []string) ([]Name, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	selected := make([]Name, 0, len(raw))
	seen := make(map[Name]struct{}, len(raw))
	for _, value := range raw {
		name := Name(strings.TrimSpace(value))
		if _, ok := findCatalogEntry(name); !ok {
			return nil, invalidOutputError(value)
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
