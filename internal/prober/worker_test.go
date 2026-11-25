package prober

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lnrdll/prober/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func zeroJitter() time.Duration { return 0 }

func TestTargetTimeout(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	defaultRunner := Runner{Jitter: zeroJitter}

	runSingle := func(t *testing.T, runner Runner, target config.Target) Result {
		t.Helper()
		if target.Name == "" {
			target.Name = "test"
		}
		plan, err := Prepare(config.Config{Targets: []config.Target{target}})
		require.NoError(t, err)
		summary := runner.Execute(context.Background(), plan, nil)
		require.Len(t, summary.Results, 1)
		return summary.Results[0]
	}

	t.Run("defaults method to GET", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		result := runSingle(t, defaultRunner, config.Target{URL: server.URL})
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

		result := runSingle(t, defaultRunner, config.Target{URL: server.URL})
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
		result := runSingle(t, defaultRunner, target)
		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
	})

	t.Run("blocks SSRF metadata hosts without sending or retrying", func(t *testing.T) {
		ssrfHosts := []string{
			"http://169.254.169.254/latest/meta-data/",
			"http://METADATA.google.internal/computeMetadata/v1/",
			"http://metadata.google.internal./computeMetadata/v1/",
			"http://metadata.goog/computeMetadata/v1/",
			"http://[::ffff:169.254.169.254]/latest/meta-data/",
			"http://[fd00:ec2::254]/latest/meta-data/",
			"http://169.254.170.2/v2/credentials",
			"http://100.100.100.200/latest/meta-data/",
		}
		for _, host := range ssrfHosts {
			t.Run(fmt.Sprintf("blocks %s", host), func(t *testing.T) {
				var requests atomic.Int32
				runner := Runner{
					Jitter: zeroJitter,
					Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
						requests.Add(1)
						return nil, fmt.Errorf("unexpected request to %s", r.URL)
					}),
				}

				target := config.Target{URL: host, Retries: 2}
				result := runSingle(t, runner, target)
				assert.Equal(t, errSSRFBlocked.Error(), result.Error)
				assert.False(t, result.Up)
				assert.Equal(t, int32(0), requests.Load())
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

		result := runSingle(t, defaultRunner, config.Target{URL: server.URL})

		assert.Empty(t, result.Error)
		assert.False(t, result.Up) // 418 fails the default assertion
		assert.Equal(t, "status >= 200 && status < 400", result.FailedAssertion)
		assert.Equal(t, http.StatusTeapot, result.Status)
		assert.Contains(t, result.Body, expectedBody) // Body is truncated to 1024 bytes
		assert.Contains(t, result.Headers, expectedHeaderKey)
		assert.Equal(t, expectedHeaderValue, result.Headers[expectedHeaderKey])
		assert.GreaterOrEqual(t, result.LatencyMS, int64(0))
	})

	t.Run("marks Up=false and FailedAssertion on failed assertion", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		target := config.Target{
			URL:        server.URL,
			Assertions: []string{"status == 404"},
		}
		result := runSingle(t, defaultRunner, target)

		assert.Empty(t, result.Error)
		assert.False(t, result.Up)
		assert.Equal(t, "status == 404", result.FailedAssertion)
	})

	t.Run("handles invalid URL / request creation failure", func(t *testing.T) {
		// Prepare rejects a malformed URL, so build the target by hand to reach
		// the probe-time failure.
		target := preparedTarget{Target: config.Target{URL: "://invalid-url"}}
		result := probeTarget(context.Background(), target, &http.Client{}, time.Now)

		assert.NotEmpty(t, result.Error)
		assert.Contains(t, result.Error, "missing protocol scheme")
		assert.False(t, result.Up)
	})

	t.Run("handles timeout/cancel path", func(t *testing.T) {
		runner := Runner{
			Jitter: zeroJitter,
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				<-r.Context().Done()
				return nil, r.Context().Err()
			}),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		plan, err := Prepare(config.Config{Targets: []config.Target{{Name: "slow", URL: "http://example.com", Timeout: 10}}})
		require.NoError(t, err)
		summary := runner.Execute(ctx, plan, nil)
		require.Len(t, summary.Results, 1)
		result := summary.Results[0]

		assert.NotEmpty(t, result.Error)
		assert.Contains(t, result.Error, "context deadline exceeded")
		assert.False(t, result.Up)
	})

	t.Run("handles body read errors", func(t *testing.T) {
		runner := Runner{
			Jitter: zeroJitter,
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       failingReadCloser{err: errors.New("connection reset by peer")},
				}, nil
			}),
		}

		result := runSingle(t, runner, config.Target{URL: "http://example.com"})
		assert.False(t, result.Up)
		assert.Contains(t, result.Error, "connection reset by peer")
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
		result := runSingle(t, defaultRunner, target)
		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
	})

	t.Run("handles SSL information capture", func(t *testing.T) {
		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer tlsServer.Close()

		runner := Runner{
			Jitter:    zeroJitter,
			Transport: tlsServer.Client().Transport,
		}
		result := runSingle(t, runner, config.Target{URL: tlsServer.URL})

		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
		assert.GreaterOrEqual(t, result.SSLDaysLeft, 0)
		assert.NotEmpty(t, result.SSLIssuer)
		assert.NotEmpty(t, result.SSLSubject)
	})

	t.Run("handles SSLSkipVerify for insecure client", func(t *testing.T) {
		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer tlsServer.Close()

		result := runSingle(t, defaultRunner, config.Target{
			URL:           tlsServer.URL,
			SSLSkipVerify: true,
		})

		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
		assert.GreaterOrEqual(t, result.SSLDaysLeft, 0)
		assert.NotEmpty(t, result.SSLIssuer)
		assert.NotEmpty(t, result.SSLSubject)
	})

	t.Run("strips port from Host header for TLS SNI", func(t *testing.T) {
		var gotHost, gotSNI string
		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotHost = r.Host
			if r.TLS != nil {
				gotSNI = r.TLS.ServerName
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer tlsServer.Close()

		runner := Runner{
			Jitter:    zeroJitter,
			Transport: tlsServer.Client().Transport,
		}
		result := runSingle(t, runner, config.Target{
			URL:     tlsServer.URL,
			Headers: map[string]string{"Host": "example.com:8443"},
		})

		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
		assert.Equal(t, "example.com:8443", gotHost)
		assert.Equal(t, "example.com", gotSNI)
	})

	t.Run("retries until success", func(t *testing.T) {
		var attempts atomic.Int32
		runner := Runner{
			Jitter: zeroJitter,
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				status := http.StatusOK
				if attempts.Add(1) == 1 {
					status = http.StatusInternalServerError
				}
				return &http.Response{
					StatusCode: status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		target := config.Target{
			URL:        "http://example.com",
			Retries:    1,
			Assertions: []string{"status == 200"},
		}
		result := runSingle(t, runner, target)
		assert.True(t, result.Up)
		assert.Equal(t, int32(2), attempts.Load())
	})

	t.Run("returns failure after exhausting retries", func(t *testing.T) {
		var attempts atomic.Int32
		runner := Runner{
			Jitter: zeroJitter,
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				attempts.Add(1)
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		target := config.Target{
			URL:        "http://example.com",
			Retries:    2,
			Assertions: []string{"status == 200"},
		}
		result := runSingle(t, runner, target)
		assert.False(t, result.Up)
		assert.Equal(t, "status == 200", result.FailedAssertion)
		assert.Equal(t, int32(3), attempts.Load())
	})
}

func TestRunnerConcurrencyAndCancellation(t *testing.T) {
	t.Parallel()

	t.Run("bounds concurrent probes to MaxConcurrency", func(t *testing.T) {
		var inFlight, maxInFlight atomic.Int32
		entered := make(chan struct{}, 6)
		release := make(chan struct{})

		runner := Runner{
			Jitter:         zeroJitter,
			MaxConcurrency: 2,
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				cur := inFlight.Add(1)
				for {
					prev := maxInFlight.Load()
					if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
						break
					}
				}
				entered <- struct{}{}
				<-release
				inFlight.Add(-1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		targets := make([]config.Target, 6)
		for i := range targets {
			targets[i] = config.Target{Name: fmt.Sprintf("t-%d", i), URL: "http://example.com"}
		}
		plan, err := Prepare(config.Config{Targets: targets})
		require.NoError(t, err)

		done := make(chan Summary, 1)
		go func() {
			done <- runner.Execute(context.Background(), plan, nil)
		}()

		<-entered
		<-entered
		assert.Equal(t, int32(2), inFlight.Load())
		close(release)

		summary := <-done
		assert.Equal(t, 6, summary.Passed)
		assert.Equal(t, int32(2), maxInFlight.Load())
	})

	t.Run("cancels during jitter without sending requests", func(t *testing.T) {
		var requests atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		runner := Runner{
			Jitter: func() time.Duration { return 10 * time.Second },
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				requests.Add(1)
				return nil, errors.New("should not run")
			}),
		}

		plan, err := Prepare(config.Config{Targets: []config.Target{
			{Name: "a", URL: "http://example.com"},
			{Name: "b", URL: "http://example.com", Disabled: true},
		}})
		require.NoError(t, err)

		summary := runner.Execute(ctx, plan, nil)
		assert.Equal(t, int32(0), requests.Load())
		assert.Equal(t, 2, summary.Total)
		assert.Equal(t, 0, summary.Passed)
		assert.Equal(t, 1, summary.Failed)
		assert.Equal(t, 1, summary.Skipped)
		require.Len(t, summary.Results, 1)
		assert.Contains(t, summary.Results[0].Error, "context canceled")
	})

	t.Run("uses injected clock for timestamps and durations", func(t *testing.T) {
		base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		var ticks atomic.Int64
		runner := Runner{
			Jitter: zeroJitter,
			Now: func() time.Time {
				return base.Add(time.Duration(ticks.Add(1)) * 10 * time.Millisecond)
			},
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("")),
					TLS:        &tls.ConnectionState{},
				}, nil
			}),
		}

		plan, err := Prepare(config.Config{Targets: []config.Target{{Name: "a", URL: "http://example.com"}}})
		require.NoError(t, err)

		summary := runner.Execute(context.Background(), plan, nil)
		require.Len(t, summary.Results, 1)
		assert.Equal(t, 2026, summary.Results[0].Timestamp.Year())
		assert.Greater(t, summary.Results[0].LatencyMS, int64(0))
		assert.Greater(t, summary.Duration, time.Duration(0))
	})
}

func TestBlockMetadataDial(t *testing.T) {
	t.Parallel()

	blocked := []string{
		"169.254.169.254:80",
		"169.254.169.254:443",
		"[::ffff:169.254.169.254]:80",
		"[fd00:ec2::254]:80",
		"169.254.170.2:80",
		"100.100.100.200:80",
	}
	for _, addr := range blocked {
		t.Run("blocks "+addr, func(t *testing.T) {
			err := blockMetadataDial("tcp", addr, nil)
			assert.ErrorIs(t, err, errSSRFBlocked)
		})
	}

	allowed := []string{
		"127.0.0.1:8080",
		"[::1]:8080",
		"93.184.216.34:443",
	}
	for _, addr := range allowed {
		t.Run("allows "+addr, func(t *testing.T) {
			assert.NoError(t, blockMetadataDial("tcp", addr, nil))
		})
	}
}

func TestExtractServerName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "example.com", extractServerName("example.com"))
	assert.Equal(t, "example.com", extractServerName("example.com:8443"))
	assert.Equal(t, "::1", extractServerName("[::1]:8443"))
	assert.Equal(t, "::1", extractServerName("[::1]"))
}

func TestExecuteSummary(t *testing.T) {
	t.Parallel()
	runner := Runner{
		Jitter: zeroJitter,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}),
	}

	plan, err := Prepare(config.Config{Targets: []config.Target{
		{Name: "pass", URL: "http://example.com", Assertions: []string{"status == 200"}},
		{Name: "fail", URL: "http://example.com", Assertions: []string{"status == 500"}},
		{Name: "skip", URL: "http://example.com", Disabled: true},
	}})
	require.NoError(t, err)

	var published []string
	summary := runner.Execute(context.Background(), plan, func(res Result) {
		published = append(published, res.Name)
	})

	assert.Equal(t, 3, summary.Total)
	assert.Equal(t, 1, summary.Passed)
	assert.Equal(t, 1, summary.Failed)
	assert.Equal(t, 1, summary.Skipped)
	assert.Len(t, summary.Results, 2)
	assert.ElementsMatch(t, []string{"pass", "fail"}, published)
	assert.GreaterOrEqual(t, summary.Duration, time.Duration(0))
}

func TestExecuteRespectsCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	plan, err := Prepare(config.Config{Targets: []config.Target{{Name: "a", URL: "http://example.com"}}})
	require.NoError(t, err)

	summary := Execute(ctx, plan, nil)
	assert.Equal(t, 1, summary.Total)
	assert.Equal(t, 1, summary.Failed)
	require.Len(t, summary.Results, 1)
	assert.Contains(t, summary.Results[0].Error, "context canceled")
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type failingReadCloser struct {
	err error
}

func (f failingReadCloser) Read([]byte) (int, error) { return 0, f.err }
func (f failingReadCloser) Close() error             { return nil }
