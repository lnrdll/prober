package prober

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"cel.dev/cel-go/cel"
	"github.com/goccy/go-yaml"
)

const (
	defaultAssertion     = "status >= 200 && status < 400"
	defaultTargetTimeout = 60 * time.Second
)

type manifest struct {
	Targets []manifestTarget `yaml:"targets"`
}

type manifestTarget struct {
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
}

// Plan is the prepared form of a manifest's targets. Only Prepare builds one,
// and Execute accepts nothing else.
type Plan struct {
	targets []preparedTarget
}

// preparedTarget is a normalized target with its compiled assertions. Disabled
// targets have no compiled assertions.
type preparedTarget struct {
	name          string
	url           string
	parsedURL     *url.URL
	method        string
	timeout       time.Duration
	attempts      int
	disabled      bool
	sslSkipVerify bool
	headers       map[string]string
	body          string
	tags          map[string]string
	assertions    []assertion
}

// Prepare loads the manifest at path, checks its structure, applies target
// defaults, and compiles the assertions of every enabled target. Disabled
// targets are checked structurally but their assertions are not compiled.
func Prepare(path string) (Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Plan{}, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg manifest
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Plan{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	targets, err := normalizeTargets(cfg.Targets)
	if err != nil {
		return Plan{}, fmt.Errorf("validate config: %w", err)
	}

	env, err := newCELEnv()
	if err != nil {
		return Plan{}, fmt.Errorf("failed to build restricted CEL env: %w", err)
	}

	for i, target := range cfg.Targets {
		if target.Disabled {
			continue
		}

		exprs := target.Assertions
		if len(exprs) == 0 {
			exprs = []string{defaultAssertion}
		}

		assertions := make([]assertion, 0, len(exprs))
		for _, expr := range exprs {
			ast, iss := env.Compile(expr)
			if iss.Err() != nil {
				return Plan{}, fmt.Errorf("compile assertions: target %q failed compiling assertion %q: %v", target.Name, expr, iss.Err())
			}
			// A dynamic output passes here; worker checks its value at run time.
			if out := ast.OutputType(); !out.IsExactType(cel.BoolType) && !out.IsExactType(cel.DynType) {
				return Plan{}, fmt.Errorf("compile assertions: target %q assertion %q must return a boolean", target.Name, expr)
			}

			prog, err := env.Program(ast)
			if err != nil {
				return Plan{}, fmt.Errorf("compile assertions: target %q failed compiling assertion %q: %w", target.Name, expr, err)
			}
			assertions = append(assertions, assertion{text: expr, program: prog})
		}

		targets[i].assertions = assertions
	}

	return Plan{targets: targets}, nil
}

func normalizeTargets(raw []manifestTarget) ([]preparedTarget, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("at least one target is required")
	}

	seenNames := make(map[string]struct{}, len(raw))
	targets := make([]preparedTarget, len(raw))
	for i, target := range raw {
		if strings.TrimSpace(target.Name) == "" {
			return nil, fmt.Errorf("target %d: name is required", i)
		}
		if _, ok := seenNames[target.Name]; ok {
			return nil, fmt.Errorf("target %q: duplicate name", target.Name)
		}
		seenNames[target.Name] = struct{}{}

		if strings.TrimSpace(target.URL) == "" {
			return nil, fmt.Errorf("target %q: url is required", target.Name)
		}
		parsedURL, err := url.Parse(target.URL)
		if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
			return nil, fmt.Errorf("target %q: invalid url %q", target.Name, target.URL)
		}
		if scheme := strings.ToLower(parsedURL.Scheme); scheme != "http" && scheme != "https" {
			return nil, fmt.Errorf("target %q: unsupported url scheme %q", target.Name, parsedURL.Scheme)
		}

		method := target.Method
		if method == "" {
			method = http.MethodGet
		} else {
			upper := strings.ToUpper(method)
			if upper != method {
				return nil, fmt.Errorf("target %q: method must be uppercase", target.Name)
			}
			if !validHTTPMethod(method) {
				return nil, fmt.Errorf("target %q: unsupported method %q", target.Name, target.Method)
			}
		}

		if target.Timeout < 0 {
			return nil, fmt.Errorf("target %q: timeout must be greater than or equal to 0", target.Name)
		}
		timeout := defaultTargetTimeout
		if target.Timeout > 0 {
			timeout = time.Duration(target.Timeout) * time.Second
		}

		if target.Retries < 0 {
			return nil, fmt.Errorf("target %q: retries must be greater than or equal to 0", target.Name)
		}

		for headerName := range target.Headers {
			if strings.TrimSpace(headerName) == "" {
				return nil, fmt.Errorf("target %q: header name cannot be empty", target.Name)
			}
		}

		targets[i] = preparedTarget{
			name:          target.Name,
			url:           target.URL,
			parsedURL:     parsedURL,
			method:        method,
			timeout:       timeout,
			attempts:      target.Retries + 1,
			disabled:      target.Disabled,
			sslSkipVerify: target.SSLSkipVerify,
			headers:       target.Headers,
			body:          target.Body,
			tags:          target.Tags,
		}
	}

	return targets, nil
}

func validHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}
