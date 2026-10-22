package prober

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
	plan, err := Prepare(cfg)
	require.NoError(t, err)
	return Execute(plan, func(output.Result) {})
}

// resultsByName indexes a summary's results by target name.
func resultsByName(summary output.Summary) map[string]output.Result {
	results := make(map[string]output.Result, len(summary.Results))
	for _, res := range summary.Results {
		results[res.Name] = res
	}
	return results
}

func TestPrepare(t *testing.T) {
	t.Run("applies the default assertion to targets without assertions", func(t *testing.T) {
		serverError := newStatusServer(t, http.StatusInternalServerError)
		teapot := newStatusServer(t, http.StatusTeapot)
		noContent := newStatusServer(t, http.StatusNoContent)

		summary := prepareAndExecute(t, config.Config{Targets: []config.Target{
			{Name: "server-error", URL: serverError.URL},
			{Name: "teapot", URL: teapot.URL},
			{Name: "no-content", URL: noContent.URL},
		}})

		assert.Equal(t, 1, summary.Passed)
		assert.Equal(t, 2, summary.Failed)
		results := resultsByName(summary)
		for _, name := range []string{"server-error", "teapot"} {
			assert.False(t, results[name].Up, name)
			assert.Equal(t, "status >= 200 && status < 400", results[name].FailedAssertion, name)
		}
		assert.True(t, results["no-content"].Up)
		assert.Empty(t, results["no-content"].FailedAssertion)
	})

	t.Run("reports the first failing assertion of several", func(t *testing.T) {
		server := newStatusServer(t, http.StatusOK)

		summary := prepareAndExecute(t, config.Config{Targets: []config.Target{
			{Name: "passing", URL: server.URL, Assertions: []string{"status == 200", "status < 300"}},
			{Name: "failing", URL: server.URL, Assertions: []string{"status == 200", "status == 500", "status == 404"}},
		}})

		results := resultsByName(summary)
		assert.True(t, results["passing"].Up)
		assert.Empty(t, results["passing"].FailedAssertion)
		assert.False(t, results["failing"].Up)
		assert.Equal(t, "status == 500", results["failing"].FailedAssertion)
	})

	t.Run("exposes every response variable to assertions", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Probe", "ok")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("hello prober"))
		}))
		t.Cleanup(server.Close)

		// The httptest certificate is issued by and to "Acme Co" for example.com.
		assertions := map[string]string{
			"status":        "status == 202",
			"body":          "body.contains('hello prober')",
			"headers":       "headers['X-Probe'] == 'ok'",
			"latency_ms":    "latency_ms >= 0 && latency_ms < 10000",
			"ssl_days_left": "ssl_days_left > 0",
			"ssl_issuer":    "ssl_issuer.contains('Acme Co')",
			"ssl_subject":   "ssl_subject.contains('Acme Co')",
			"ssl_dns_names": "ssl_dns_names.contains('example.com')",
		}
		var targets []config.Target
		for name, expr := range assertions {
			targets = append(targets, config.Target{Name: name, URL: server.URL, SSLSkipVerify: true, Assertions: []string{expr}})
		}

		summary := prepareAndExecute(t, config.Config{Targets: targets})

		results := resultsByName(summary)
		require.Len(t, results, len(assertions))
		for name, res := range results {
			assert.Empty(t, res.Error, name)
			assert.True(t, res.Up, "%s: %s failed", name, res.FailedAssertion)
		}
	})

	t.Run("preparing twice reports a failing assertion once with its text", func(t *testing.T) {
		server := newStatusServer(t, http.StatusOK)
		cfg := config.Config{Targets: []config.Target{
			{Name: "failing", URL: server.URL, Assertions: []string{"status == 200", "status == 500"}},
			{Name: "defaulted", URL: server.URL},
		}}

		for range 2 {
			summary := prepareAndExecute(t, cfg)

			require.Len(t, summary.Results, 2)
			results := resultsByName(summary)
			assert.False(t, results["failing"].Up)
			assert.Equal(t, "status == 500", results["failing"].FailedAssertion)
			assert.Equal(t, 1, summary.Failed)
		}
		assert.Equal(t, []string{"status == 200", "status == 500"}, cfg.Targets[0].Assertions)
		assert.Nil(t, cfg.Targets[1].Assertions)
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

	t.Run("rejects assertions that do not return a boolean", func(t *testing.T) {
		_, err := Prepare(config.Config{Targets: []config.Target{
			{Name: "test", URL: "http://example.com", Assertions: []string{"status == 200", "status + 1"}},
		}})
		assert.EqualError(t, err, `compile assertions: target "test" assertion "status + 1" must return a boolean`)
	})

	t.Run("accepts boolean and dynamic assertions", func(t *testing.T) {
		server := newStatusServer(t, http.StatusOK)

		summary := prepareAndExecute(t, config.Config{Targets: []config.Target{
			{Name: "test", URL: server.URL, Assertions: []string{"status == 200", "dyn(status == 200)"}},
		}})

		assert.Equal(t, 1, summary.Passed)
	})

	t.Run("leaves dynamic assertions that yield a non-boolean to execution", func(t *testing.T) {
		server := newStatusServer(t, http.StatusOK)

		summary := prepareAndExecute(t, config.Config{Targets: []config.Target{
			{Name: "test", URL: server.URL, Assertions: []string{"dyn(status + 1)"}},
		}})

		require.Len(t, summary.Results, 1)
		assert.False(t, summary.Results[0].Up)
		assert.Equal(t, "dyn(status + 1)", summary.Results[0].FailedAssertion)
		assert.Equal(t, 1, summary.Failed)
	})

	t.Run("does not reject non-boolean assertions on disabled targets", func(t *testing.T) {
		_, err := Prepare(config.Config{Targets: []config.Target{
			{Name: "disabled", URL: "http://example.com", Disabled: true, Assertions: []string{"status + 1"}},
		}})
		assert.NoError(t, err)
	})
}
