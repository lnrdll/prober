package config

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func Validate(cfg Config) error {
	if len(cfg.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}

	seenNames := map[string]struct{}{}
	for i, target := range cfg.Targets {
		label := target.Name
		if label == "" {
			label = fmt.Sprintf("targets[%d]", i)
		}

		if strings.TrimSpace(target.Name) == "" {
			return fmt.Errorf("target %d: name is required", i)
		}
		if _, ok := seenNames[target.Name]; ok {
			return fmt.Errorf("target %q: duplicate name", target.Name)
		}
		seenNames[target.Name] = struct{}{}

		if strings.TrimSpace(target.URL) == "" {
			return fmt.Errorf("target %q: url is required", label)
		}
		parsedURL, err := url.Parse(target.URL)
		if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
			return fmt.Errorf("target %q: invalid url %q", label, target.URL)
		}

		if target.Method != "" {
			method := strings.ToUpper(target.Method)
			if method != target.Method {
				return fmt.Errorf("target %q: method must be uppercase", label)
			}
			if !validHTTPMethod(method) {
				return fmt.Errorf("target %q: unsupported method %q", label, target.Method)
			}
		}

		if target.Timeout < 0 {
			return fmt.Errorf("target %q: timeout must be greater than or equal to 0", label)
		}
		if target.Retries < 0 {
			return fmt.Errorf("target %q: retries must be greater than or equal to 0", label)
		}

		for headerName := range target.Headers {
			if strings.TrimSpace(headerName) == "" {
				return fmt.Errorf("target %q: header name cannot be empty", label)
			}
		}
	}

	return nil
}

func validHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}
