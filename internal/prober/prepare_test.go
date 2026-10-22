package prober

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"cel.dev/cel-go/cel"
	"github.com/lnrdll/prober/internal/config"
	"github.com/lnrdll/prober/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newStatusServer starts a local server that answers every request with status.
func newStatusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server
}

// prepareAndExecute prepares cfg and executes the result, failing the test if
// preparation fails.
func prepareAndExecute(t *testing.T, cfg config.Config) output.Summary {
	t.Helper()
	targets, err := Prepare(cfg)
	require.NoError(t, err)
	return Execute(targets, func(output.Result) {})
}

func TestPrepare(t *testing.T) {
	t.Run("applies the default assertion to targets without assertions", func(t *testing.T) {
		failing := newStatusServer(t, http.StatusInternalServerError)
		passing := newStatusServer(t, http.StatusNoContent)

		summary := prepareAndExecute(t, config.Config{Targets: []config.Target{
			{Name: "failing", URL: failing.URL},
			{Name: "passing", URL: passing.URL},
		}})

		assert.Equal(t, 1, summary.Passed)
		assert.Equal(t, 1, summary.Failed)
		for _, res := range summary.Results {
			if res.Name == "failing" {
				assert.Equal(t, "status >= 200 && status < 400", res.FailedAssertion)
			}
		}
	})

	t.Run("preparing twice reports a failing assertion once with its text", func(t *testing.T) {
		server := newStatusServer(t, http.StatusOK)
		existing := []cel.Program{nil}
		cfg := config.Config{Targets: []config.Target{
			{Name: "failing", URL: server.URL, Assertions: []string{"status == 200", "status == 500"}, CompiledCel: existing},
			{Name: "defaulted", URL: server.URL},
		}}

		for range 2 {
			summary := prepareAndExecute(t, cfg)

			require.Len(t, summary.Results, 2)
			for _, res := range summary.Results {
				if res.Name == "failing" {
					assert.False(t, res.Up)
					assert.Equal(t, "status == 500", res.FailedAssertion)
				}
			}
			assert.Equal(t, 1, summary.Failed)
		}
		assert.Equal(t, []string{"status == 200", "status == 500"}, cfg.Targets[0].Assertions)
		require.Len(t, cfg.Targets[0].CompiledCel, 1)
		assert.Nil(t, cfg.Targets[0].CompiledCel[0])
		assert.Equal(t, existing, cfg.Targets[0].CompiledCel)
		assert.Nil(t, cfg.Targets[1].Assertions)
		assert.Nil(t, cfg.Targets[1].CompiledCel)
	})

	t.Run("skips disabled targets without compiling them", func(t *testing.T) {
		server := newStatusServer(t, http.StatusOK)

		summary := prepareAndExecute(t, config.Config{Targets: []config.Target{
			{Name: "enabled", URL: server.URL},
			{Name: "disabled", URL: server.URL, Disabled: true, Assertions: []string{"status === 200"}},
		}})

		assert.Equal(t, 2, summary.Total)
		assert.Equal(t, 1, summary.Passed)
		assert.Equal(t, 1, summary.Skipped)
	})

	t.Run("validates disabled targets", func(t *testing.T) {
		_, err := Prepare(config.Config{Targets: []config.Target{
			{Name: "disabled", URL: "http://example.com", Method: "get", Disabled: true},
		}})
		assert.EqualError(t, err, `validate config: target "disabled": method must be uppercase`)
	})

	t.Run("returns validation errors before compiling", func(t *testing.T) {
		_, err := Prepare(config.Config{Targets: []config.Target{
			{Name: "test", URL: "http://example.com", Method: "get", Assertions: []string{"status === 200"}},
		}})
		assert.EqualError(t, err, `validate config: target "test": method must be uppercase`)
	})

	t.Run("returns compile errors with target and expression", func(t *testing.T) {
		_, err := Prepare(config.Config{Targets: []config.Target{
			{Name: "test", URL: "http://example.com", Assertions: []string{"status === 200"}},
		}})
		require.Error(t, err)
		assert.Regexp(t, `^compile assertions: target "test" failed compiling assertion "status === 200": .*Syntax error`, err.Error())
	})
}
