package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	validConfig := Config{Targets: []Target{{Name: "api", URL: "https://example.com", Method: "GET", Timeout: 10, Headers: map[string]string{"X-Test": "1"}}}}

	t.Run("valid config", func(t *testing.T) {
		require.NoError(t, Validate(validConfig))
	})

	t.Run("requires target", func(t *testing.T) {
		assert.ErrorContains(t, Validate(Config{}), "at least one target is required")
	})

	t.Run("requires target name", func(t *testing.T) {
		cfg := Config{Targets: []Target{{URL: "https://example.com"}}}
		assert.ErrorContains(t, Validate(cfg), "name is required")
	})

	t.Run("requires unique target name", func(t *testing.T) {
		cfg := Config{Targets: []Target{{Name: "dup", URL: "https://one.example.com"}, {Name: "dup", URL: "https://two.example.com"}}}
		assert.ErrorContains(t, Validate(cfg), "duplicate name")
	})

	t.Run("requires url", func(t *testing.T) {
		cfg := Config{Targets: []Target{{Name: "api"}}}
		assert.ErrorContains(t, Validate(cfg), "url is required")
	})

	t.Run("rejects invalid url", func(t *testing.T) {
		cfg := Config{Targets: []Target{{Name: "api", URL: "://bad"}}}
		assert.ErrorContains(t, Validate(cfg), "invalid url")
	})

	t.Run("rejects lowercase method", func(t *testing.T) {
		cfg := Config{Targets: []Target{{Name: "api", URL: "https://example.com", Method: "get"}}}
		assert.ErrorContains(t, Validate(cfg), "method must be uppercase")
	})

	t.Run("rejects unsupported method", func(t *testing.T) {
		cfg := Config{Targets: []Target{{Name: "api", URL: "https://example.com", Method: "TRACE"}}}
		assert.ErrorContains(t, Validate(cfg), "unsupported method")
	})

	t.Run("rejects negative timeout", func(t *testing.T) {
		cfg := Config{Targets: []Target{{Name: "api", URL: "https://example.com", Timeout: -1}}}
		assert.ErrorContains(t, Validate(cfg), "timeout must be greater than or equal to 0")
	})

	t.Run("rejects negative retries", func(t *testing.T) {
		cfg := Config{Targets: []Target{{Name: "api", URL: "https://example.com", Retries: -1}}}
		assert.ErrorContains(t, Validate(cfg), "retries must be greater than or equal to 0")
	})

	t.Run("rejects empty header name", func(t *testing.T) {
		cfg := Config{Targets: []Target{{Name: "api", URL: "https://example.com", Headers: map[string]string{"": "x"}}}}
		assert.ErrorContains(t, Validate(cfg), "header name cannot be empty")
	})
}
