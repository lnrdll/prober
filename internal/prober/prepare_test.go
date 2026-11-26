package prober

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeManifestFile writes raw YAML content into a temporary file and returns its path.
func writeManifestFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "targets.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

// newStatusServer starts a local server that answers every request with status.
func newStatusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server
}

// prepareAndExecute prepares the manifest content and executes the resulting
// plan, failing the test if preparation fails.
func prepareAndExecute(t *testing.T, content string) Summary {
	t.Helper()
	return preparePathAndExecute(t, writeManifestFile(t, content))
}

func preparePathAndExecute(t *testing.T, path string) Summary {
	t.Helper()
	plan, err := Prepare(path)
	require.NoError(t, err)
	runner := Runner{Jitter: func() time.Duration { return 0 }}
	return runner.Execute(context.Background(), plan, nil)
}

// resultsByName indexes a summary's results by target name.
func resultsByName(summary Summary) map[string]Result {
	results := make(map[string]Result, len(summary.Results))
	for _, res := range summary.Results {
		results[res.Name] = res
	}
	return results
}

func TestPrepare(t *testing.T) {
	t.Parallel()

	t.Run("wraps file read errors", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "missing.yaml")
		_, err := Prepare(missing)
		require.Error(t, err)
		assert.ErrorContains(t, err, fmt.Sprintf("read config %q:", missing))
	})

	t.Run("wraps YAML parse errors", func(t *testing.T) {
		t.Parallel()
		path := writeManifestFile(t, "targets: [")
		_, err := Prepare(path)
		require.Error(t, err)
		assert.ErrorContains(t, err, fmt.Sprintf("parse config %q:", path))
	})

	t.Run("applies the default assertion to targets without assertions", func(t *testing.T) {
		t.Parallel()
		serverError := newStatusServer(t, http.StatusInternalServerError)
		teapot := newStatusServer(t, http.StatusTeapot)
		noContent := newStatusServer(t, http.StatusNoContent)

		summary := prepareAndExecute(t, fmt.Sprintf(`targets:
  - name: server-error
    url: %s
  - name: teapot
    url: %s
  - name: no-content
    url: %s
`, serverError.URL, teapot.URL, noContent.URL))

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
		t.Parallel()
		server := newStatusServer(t, http.StatusOK)

		summary := prepareAndExecute(t, fmt.Sprintf(`targets:
  - name: passing
    url: %s
    assertions:
      - status == 200
      - status < 300
  - name: failing
    url: %s
    assertions:
      - status == 200
      - status == 500
      - status == 404
`, server.URL, server.URL))

		results := resultsByName(summary)
		assert.True(t, results["passing"].Up)
		assert.Empty(t, results["passing"].FailedAssertion)
		assert.False(t, results["failing"].Up)
		assert.Equal(t, "status == 500", results["failing"].FailedAssertion)
	})

	t.Run("exposes every response variable to assertions", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Probe", "ok")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("hello prober"))
		}))
		t.Cleanup(server.Close)

		// The httptest certificate is issued by and to "Acme Co" for example.com.
		assertions := []struct {
			name string
			expr string
		}{
			{name: "status", expr: "status == 202"},
			{name: "body", expr: "body.contains('hello prober')"},
			{name: "headers", expr: "headers['X-Probe'] == 'ok'"},
			{name: "latency_ms", expr: "latency_ms >= 0 && latency_ms < 10000"},
			{name: "ssl_days_left", expr: "ssl_days_left > 0"},
			{name: "ssl_issuer", expr: "ssl_issuer.contains('Acme Co')"},
			{name: "ssl_subject", expr: "ssl_subject.contains('Acme Co')"},
			{name: "ssl_dns_names", expr: "ssl_dns_names.contains('example.com')"},
		}
		var b strings.Builder
		b.WriteString("targets:\n")
		for _, tc := range assertions {
			fmt.Fprintf(&b, "  - name: %s\n    url: %s\n    ssl_skip_verify: true\n    assertions:\n      - %q\n", tc.name, server.URL, tc.expr)
		}

		summary := prepareAndExecute(t, b.String())

		results := resultsByName(summary)
		require.Len(t, results, len(assertions))
		for _, tc := range assertions {
			res := results[tc.name]
			assert.Empty(t, res.Error, tc.name)
			assert.True(t, res.Up, "%s: %s failed", tc.name, res.FailedAssertion)
		}
	})

	t.Run("preparing twice reports a failing assertion once with its text", func(t *testing.T) {
		t.Parallel()
		server := newStatusServer(t, http.StatusOK)
		path := writeManifestFile(t, fmt.Sprintf(`targets:
  - name: failing
    url: %s
    assertions:
      - status == 200
      - status == 500
  - name: defaulted
    url: %s
`, server.URL, server.URL))

		for range 2 {
			summary := preparePathAndExecute(t, path)

			require.Len(t, summary.Results, 2)
			results := resultsByName(summary)
			assert.False(t, results["failing"].Up)
			assert.Equal(t, "status == 500", results["failing"].FailedAssertion)
			assert.Equal(t, 1, summary.Failed)
		}
	})

	t.Run("skips disabled targets without compiling them", func(t *testing.T) {
		t.Parallel()
		server := newStatusServer(t, http.StatusOK)

		summary := prepareAndExecute(t, fmt.Sprintf(`targets:
  - name: enabled
    url: %s
  - name: disabled-default
    url: %s
    disabled: true
    assertions:
      - status === 200
  - name: disabled-post
    url: %s
    method: POST
    disabled: true
`, server.URL, server.URL, server.URL))

		assert.Equal(t, 3, summary.Total)
		assert.Equal(t, 1, summary.Passed)
		assert.Equal(t, 2, summary.Skipped)
		require.Len(t, summary.SkippedResults, 2)
		assert.Equal(t, "disabled-default", summary.SkippedResults[0].Name)
		assert.Equal(t, http.MethodGet, summary.SkippedResults[0].Method)
		assert.Equal(t, "disabled-post", summary.SkippedResults[1].Name)
		assert.Equal(t, http.MethodPost, summary.SkippedResults[1].Method)
	})

	t.Run("enforces structural validation rules", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name     string
			manifest string
			wantErr  string
		}{
			{
				name:     "requires at least one target",
				manifest: "targets: []\n",
				wantErr:  "validate config: at least one target is required",
			},
			{
				name:     "requires target name",
				manifest: "targets:\n  - url: https://example.com\n",
				wantErr:  "validate config: target 0: name is required",
			},
			{
				name:     "rejects whitespace-only target name",
				manifest: "targets:\n  - name: \"   \"\n    url: https://example.com\n",
				wantErr:  "validate config: target 0: name is required",
			},
			{
				name: "requires unique target name",
				manifest: `targets:
  - name: dup
    url: https://one.example.com
  - name: dup
    url: https://two.example.com
`,
				wantErr: `validate config: target "dup": duplicate name`,
			},
			{
				name:     "requires url",
				manifest: "targets:\n  - name: api\n",
				wantErr:  `validate config: target "api": url is required`,
			},
			{
				name:     "rejects whitespace-only url",
				manifest: "targets:\n  - name: api\n    url: \"  \"\n",
				wantErr:  `validate config: target "api": url is required`,
			},
			{
				name:     "rejects malformed url",
				manifest: "targets:\n  - name: api\n    url: \"://bad\"\n",
				wantErr:  `validate config: target "api": invalid url "://bad"`,
			},
			{
				name:     "rejects url without scheme",
				manifest: "targets:\n  - name: api\n    url: example.com\n",
				wantErr:  `validate config: target "api": invalid url "example.com"`,
			},
			{
				name:     "rejects url without host",
				manifest: "targets:\n  - name: api\n    url: https://\n",
				wantErr:  `validate config: target "api": invalid url "https://"`,
			},
			{
				name:     "rejects ftp scheme",
				manifest: "targets:\n  - name: api\n    url: ftp://example.com/file\n",
				wantErr:  `validate config: target "api": unsupported url scheme "ftp"`,
			},
			{
				name:     "rejects file scheme",
				manifest: "targets:\n  - name: api\n    url: file://localhost/etc/passwd\n",
				wantErr:  `validate config: target "api": unsupported url scheme "file"`,
			},
			{
				name:     "rejects lowercase method",
				manifest: "targets:\n  - name: api\n    url: https://example.com\n    method: get\n",
				wantErr:  `validate config: target "api": method must be uppercase`,
			},
			{
				name:     "rejects unsupported method",
				manifest: "targets:\n  - name: api\n    url: https://example.com\n    method: TRACE\n",
				wantErr:  `validate config: target "api": unsupported method "TRACE"`,
			},
			{
				name:     "rejects negative timeout",
				manifest: "targets:\n  - name: api\n    url: https://example.com\n    timeout: -1\n",
				wantErr:  `validate config: target "api": timeout must be greater than or equal to 0`,
			},
			{
				name:     "rejects negative retries",
				manifest: "targets:\n  - name: api\n    url: https://example.com\n    retries: -1\n",
				wantErr:  `validate config: target "api": retries must be greater than or equal to 0`,
			},
			{
				name: "rejects empty header name",
				manifest: `targets:
  - name: api
    url: https://example.com
    headers:
      "": x
`,
				wantErr: `validate config: target "api": header name cannot be empty`,
			},
			{
				name: "rejects whitespace-only header name",
				manifest: `targets:
  - name: api
    url: https://example.com
    headers:
      "   ": x
`,
				wantErr: `validate config: target "api": header name cannot be empty`,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := Prepare(writeManifestFile(t, tt.manifest))
				assert.EqualError(t, err, tt.wantErr)
			})
		}
	})

	t.Run("accepts all supported uppercase methods", func(t *testing.T) {
		t.Parallel()
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			t.Run(method, func(t *testing.T) {
				t.Parallel()
				path := writeManifestFile(t, fmt.Sprintf("targets:\n  - name: api\n    url: https://example.com\n    method: %s\n", method))
				_, err := Prepare(path)
				assert.NoError(t, err)
			})
		}
	})

	t.Run("validates disabled targets", func(t *testing.T) {
		t.Parallel()
		path := writeManifestFile(t, `targets:
  - name: disabled
    url: http://example.com
    method: get
    disabled: true
`)
		_, err := Prepare(path)
		assert.EqualError(t, err, `validate config: target "disabled": method must be uppercase`)
	})

	t.Run("returns validation errors before compiling", func(t *testing.T) {
		t.Parallel()
		path := writeManifestFile(t, `targets:
  - name: test
    url: http://example.com
    method: get
    assertions:
      - status === 200
`)
		_, err := Prepare(path)
		assert.EqualError(t, err, `validate config: target "test": method must be uppercase`)
	})

	t.Run("validates every target before compiling any CEL assertions", func(t *testing.T) {
		t.Parallel()
		path := writeManifestFile(t, `targets:
  - name: first
    url: http://example.com
    assertions:
      - status === 200
  - name: second
    url: http://example.com
    method: get
`)
		_, err := Prepare(path)
		assert.EqualError(t, err, `validate config: target "second": method must be uppercase`)
	})

	t.Run("returns compile errors with target and expression", func(t *testing.T) {
		t.Parallel()
		path := writeManifestFile(t, `targets:
  - name: test
    url: http://example.com
    assertions:
      - status === 200
`)
		_, err := Prepare(path)
		require.Error(t, err)
		assert.Regexp(t, `^compile assertions: target "test" failed compiling assertion "status === 200": .*Syntax error`, err.Error())
	})

	t.Run("rejects assertions that do not return a boolean", func(t *testing.T) {
		t.Parallel()
		path := writeManifestFile(t, `targets:
  - name: test
    url: http://example.com
    assertions:
      - status == 200
      - status + 1
`)
		_, err := Prepare(path)
		assert.EqualError(t, err, `compile assertions: target "test" assertion "status + 1" must return a boolean`)
	})

	t.Run("accepts boolean and dynamic assertions", func(t *testing.T) {
		t.Parallel()
		server := newStatusServer(t, http.StatusOK)

		summary := prepareAndExecute(t, fmt.Sprintf(`targets:
  - name: test
    url: %s
    assertions:
      - status == 200
      - dyn(status == 200)
`, server.URL))

		assert.Equal(t, 1, summary.Passed)
	})

	t.Run("leaves dynamic assertions that yield a non-boolean to execution", func(t *testing.T) {
		t.Parallel()
		server := newStatusServer(t, http.StatusOK)

		summary := prepareAndExecute(t, fmt.Sprintf(`targets:
  - name: test
    url: %s
    assertions:
      - dyn(status + 1)
`, server.URL))

		require.Len(t, summary.Results, 1)
		assert.False(t, summary.Results[0].Up)
		assert.Equal(t, "dyn(status + 1)", summary.Results[0].FailedAssertion)
		assert.Equal(t, 1, summary.Failed)
	})

	t.Run("does not reject non-boolean assertions on disabled targets", func(t *testing.T) {
		t.Parallel()
		path := writeManifestFile(t, `targets:
  - name: disabled
    url: http://example.com
    disabled: true
    assertions:
      - status + 1
`)
		_, err := Prepare(path)
		assert.NoError(t, err)
	})
}
