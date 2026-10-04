package config

import "cel.dev/cel-go/cel"

type Target struct {
	Name          string            `yaml:"name"`
	URL           string            `yaml:"url"`
	Method        string            `yaml:"method"`
	Timeout       int               `yaml:"timeout"`
	Retries       int               `yaml:"retries"`
	Disabled      bool              `yaml:"disabled"`
	SSLSkipVerify bool              `yaml:"ssl_skip_verify"`
	Headers       map[string]string `yaml:"headers"`
	Body          string            `yaml:"body"`
	Assertions    []string          `yaml:"assertions"`
	Tags          map[string]string `yaml:"tags"`

	CompiledCel []cel.Program `yaml:"-"`
}

type Config struct {
	Targets []Target `yaml:"targets"`
}
