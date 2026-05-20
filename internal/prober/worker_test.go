package prober

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/lnrdll/prober/internal/config"
	"github.com/lnrdll/prober/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capturePublisher struct {
	mu      sync.Mutex
	results []output.Result
}

func (p *capturePublisher) Publish(res output.Result) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.results = append(p.results, res)
	return nil
}

func (p *capturePublisher) Close() error { return nil }

func TestTargetTimeout(t *testing.T) {
	tests := []struct {
		name          string
		targetTimeout int
		expected      time.Duration
	}{
		{
			name:          "defaults to 60s for zero timeout",
			targetTimeout: 0,
			expected:      60 * time.Second,
		},
		{
			name:          "defaults to 60s for negative timeout",
			targetTimeout: -10,
			expected:      60 * time.Second,
		},
		{
			name:          "uses explicit target timeout in seconds",
			targetTimeout: 30,
			expected:      30 * time.Second,
		},
		{
			name:          "uses explicit target timeout in seconds (large)",
			targetTimeout: 120,
			expected:      120 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := config.Target{Timeout: tt.targetTimeout}
			assert.Equal(t, tt.expected, targetTimeout(target))
		})
	}
}

func TestProbeTarget(t *testing.T) {
	// Helper to compile CEL assertions for tests
	compileAssertions := func(assertions []string) []cel.Program {
		env, _ := cel.NewEnv(
			cel.Variable("status", cel.IntType),
			cel.Variable("body", cel.StringType),
			cel.Variable("latency_ms", cel.IntType),
			cel.Variable("ssl_days_left", cel.IntType),
			cel.Variable("ssl_issuer", cel.StringType),
			cel.Variable("ssl_subject", cel.StringType),
			cel.Variable("ssl_dns_names", cel.StringType),
			cel.Variable("headers", cel.MapType(cel.StringType, cel.StringType)),
		)
		var compiledProgs []cel.Program
		for _, expr := range assertions {
			ast, iss := env.Compile(expr)
			if iss.Err() != nil {
				t.Fatalf("failed compiling assertion '%s': %s", expr, iss.Err())
			}
			prog, err := env.Program(ast)
			if err != nil {
				t.Fatalf("failed program instantiation: %s", err)
			}
			compiledProgs = append(compiledProgs, prog)
		}
		return compiledProgs
	}

	defaultClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	t.Run("defaults method to GET", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		target := config.Target{URL: server.URL}
		result := probeTarget(target, defaultClient)
		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
		assert.Equal(t, http.MethodGet, result.Method)
	})

	t.Run("sets User-Agent: prober", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "prober", r.Header.Get("User-Agent"))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		target := config.Target{URL: server.URL}
		result := probeTarget(target, defaultClient)
		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
	})

	t.Run("respects custom headers", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "custom-value", r.Header.Get("X-Custom-Header"))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		target := config.Target{
			URL:     server.URL,
			Headers: map[string]string{"X-Custom-Header": "custom-value"},
		}
		result := probeTarget(target, defaultClient)
		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
	})

	t.Run("blocks SSRF metadata hosts", func(t *testing.T) {
		ssrfHosts := []string{"http://169.254.169.254", "http://metadata.google.internal"}
		for _, host := range ssrfHosts {
			t.Run(fmt.Sprintf("blocks %s", host), func(t *testing.T) {
				target := config.Target{URL: host}
				result := probeTarget(target, defaultClient)
				assert.NotEmpty(t, result.Error)
				assert.Contains(t, result.Error, "SSRF Alert")
				assert.False(t, result.Up)
			})
		}
	})

	t.Run("captures status/body/headers/latency basics", func(t *testing.T) {
		expectedBody := "Hello, Prober!"
		expectedHeaderKey := "X-Response-Header"
		expectedHeaderValue := "response-value"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(expectedHeaderKey, expectedHeaderValue)
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte(expectedBody))
		}))
		defer server.Close()

		target := config.Target{URL: server.URL}
		result := probeTarget(target, defaultClient)

		assert.Empty(t, result.Error)
		assert.True(t, result.Up) // Default assertion passes for 418
		assert.Equal(t, http.StatusTeapot, result.Status)
		assert.Contains(t, result.Body, expectedBody) // Body is truncated to 1024 bytes
		assert.Contains(t, result.Headers, expectedHeaderKey)
		assert.Equal(t, expectedHeaderValue, result.Headers[expectedHeaderKey])
		assert.GreaterOrEqual(t, result.LatencyMS, int64(0))
	})

	t.Run("marks Up=false and FailedAssertion on failed assertion", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK) // Status 200
		}))
		defer server.Close()

		target := config.Target{
			URL:         server.URL,
			Assertions:  []string{"status == 404"}, // This will fail
			CompiledCel: compileAssertions([]string{"status == 404"}),
		}
		result := probeTarget(target, defaultClient)

		assert.Empty(t, result.Error)
		assert.False(t, result.Up)
		assert.Equal(t, "status == 404", result.FailedAssertion)
	})

	t.Run("handles invalid URL / request creation failure", func(t *testing.T) {
		target := config.Target{URL: "://invalid-url"} // Malformed URL
		result := probeTarget(target, defaultClient)

		assert.NotEmpty(t, result.Error)
		assert.Contains(t, result.Error, "missing protocol scheme")
		assert.False(t, result.Up)
	})

	t.Run("handles timeout/cancel path", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond) // Longer than target timeout
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		target := config.Target{
			URL:     server.URL,
			Timeout: 1, // 1 second timeout
		}
		// Override the client's timeout to ensure the context timeout is hit
		clientWithShortTimeout := &http.Client{
			Timeout: 100 * time.Millisecond, // Shorter than server sleep
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		}

		result := probeTarget(target, clientWithShortTimeout)

		assert.NotEmpty(t, result.Error)
		assert.Contains(t, result.Error, "context deadline exceeded")
		assert.False(t, result.Up)
	})

	t.Run("handles request body", func(t *testing.T) {
		expectedBody := "request body content"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqBody, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Equal(t, expectedBody, string(reqBody))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		target := config.Target{
			URL:    server.URL,
			Method: http.MethodPost,
			Body:   expectedBody,
		}
		result := probeTarget(target, defaultClient)
		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
	})

	t.Run("handles SSL information capture", func(t *testing.T) {
		// This test requires a TLS server, which httptest.NewTLSServer provides.
		// However, to avoid "x509: certificate signed by unknown authority" errors
		// without setting InsecureSkipVerify, we need to use the client provided by the test server.
		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer tlsServer.Close()

		target := config.Target{
			URL: tlsServer.URL,
		}

		// Use the client from the TLS server to trust its certificate
		result := probeTarget(target, tlsServer.Client())

		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
		assert.GreaterOrEqual(t, result.SSLDaysLeft, 0)
		assert.NotEmpty(t, result.SSLIssuer)
		assert.NotEmpty(t, result.SSLSubject)
		// DNSNames might be empty for a default httptest TLS server cert, so we don't assert NotEmpty
	})

	t.Run("handles SSLSkipVerify for insecure client", func(t *testing.T) {
		// This test requires a TLS server, which httptest.NewTLSServer provides.
		// We will use a custom client with InsecureSkipVerify set to true.
		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer tlsServer.Close()

		insecureClient := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		}

		target := config.Target{
			URL:           tlsServer.URL,
			SSLSkipVerify: true,
		}

		result := probeTarget(target, insecureClient)

		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
		assert.GreaterOrEqual(t, result.SSLDaysLeft, 0)
		assert.NotEmpty(t, result.SSLIssuer)
		assert.NotEmpty(t, result.SSLSubject)
	})

	t.Run("retries until success", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		target := config.Target{
			URL:         server.URL,
			Retries:     1,
			Assertions:  []string{"status == 200"},
			CompiledCel: compileAssertions([]string{"status == 200"}),
		}
		result := probeTarget(target, defaultClient)
		assert.True(t, result.Up)
		assert.Equal(t, 2, attempts)
	})

	t.Run("returns failure after exhausting retries", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		target := config.Target{
			URL:         server.URL,
			Retries:     2,
			Assertions:  []string{"status == 200"},
			CompiledCel: compileAssertions([]string{"status == 200"}),
		}
		result := probeTarget(target, defaultClient)
		assert.False(t, result.Up)
		assert.Equal(t, "status == 200", result.FailedAssertion)
		assert.Equal(t, 3, attempts)
	})
}

func TestExecuteSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	compiledTargets, err := CompileTargetAssertions([]config.Target{
		{Name: "pass", URL: server.URL, Assertions: []string{"status == 200"}},
		{Name: "fail", URL: server.URL, Assertions: []string{"status == 500"}},
		{Name: "skip", URL: server.URL, Disabled: true},
	})
	require.NoError(t, err)

	publisher := &capturePublisher{}
	summary := Execute(compiledTargets, []output.Publisher{publisher})

	assert.Equal(t, 3, summary.Total)
	assert.Equal(t, 1, summary.Passed)
	assert.Equal(t, 1, summary.Failed)
	assert.Equal(t, 1, summary.Skipped)
	assert.Len(t, summary.Results, 2)
	assert.Len(t, publisher.results, 2)
	assert.GreaterOrEqual(t, summary.Duration, time.Duration(0))
}
