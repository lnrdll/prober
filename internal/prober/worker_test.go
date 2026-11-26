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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func zeroJitter() time.Duration { return 0 }

func TestProbeTarget(t *testing.T) {
	t.Parallel()
	defaultRunner := Runner{Jitter: zeroJitter}

	runManifest := func(t *testing.T, runner Runner, content string) Result {
		t.Helper()
		plan, err := Prepare(writeManifestFile(t, content))
		require.NoError(t, err)
		summary := runner.Execute(context.Background(), plan, nil)
		require.Len(t, summary.Results, 1)
		return summary.Results[0]
	}

	t.Run("defaults method to GET", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		result := runManifest(t, defaultRunner, fmt.Sprintf("targets:\n  - name: test\n    url: %s\n", server.URL))
		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
		assert.Equal(t, http.MethodGet, result.Method)
	})

	t.Run("sets User-Agent: prober", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "prober", r.Header.Get("User-Agent"))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		result := runManifest(t, defaultRunner, fmt.Sprintf("targets:\n  - name: test\n    url: %s\n", server.URL))
		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
	})

	t.Run("respects custom headers", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "custom-value", r.Header.Get("X-Custom-Header"))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		result := runManifest(t, defaultRunner, fmt.Sprintf(`targets:
  - name: test
    url: %s
    headers:
      X-Custom-Header: custom-value
`, server.URL))
		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
	})

	t.Run("preserves target tags on result", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		result := runManifest(t, defaultRunner, fmt.Sprintf(`targets:
  - name: test
    url: %s
    tags:
      env: prod
      service: api
`, server.URL))
		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
		assert.Equal(t, map[string]string{"env": "prod", "service": "api"}, result.Tags)
	})

	t.Run("preserves raw URL string for uppercase schemes", func(t *testing.T) {
		t.Parallel()
		for _, rawURL := range []string{"HTTP://example.com/health", "HTTPS://example.com/health"} {
			t.Run(rawURL, func(t *testing.T) {
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

				result := runManifest(t, runner, fmt.Sprintf("targets:\n  - name: test\n    url: %q\n", rawURL))
				assert.Empty(t, result.Error)
				assert.True(t, result.Up)
				assert.Equal(t, rawURL, result.URL)
			})
		}
	})

	t.Run("applies default and explicit timeout deadlines to request context", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name     string
			manifest string
			expected time.Duration
		}{
			{
				name:     "omitted timeout defaults to 60s",
				manifest: "targets:\n  - name: test\n    url: http://example.com\n",
				expected: 60 * time.Second,
			},
			{
				name:     "zero timeout defaults to 60s",
				manifest: "targets:\n  - name: test\n    url: http://example.com\n    timeout: 0\n",
				expected: 60 * time.Second,
			},
			{
				name:     "explicit 30s timeout",
				manifest: "targets:\n  - name: test\n    url: http://example.com\n    timeout: 30\n",
				expected: 30 * time.Second,
			},
			{
				name:     "explicit 120s timeout",
				manifest: "targets:\n  - name: test\n    url: http://example.com\n    timeout: 120\n",
				expected: 120 * time.Second,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var (
					deadline   time.Time
					hasDL      bool
					capturedAt time.Time
				)
				runner := Runner{
					Jitter: zeroJitter,
					Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
						deadline, hasDL = r.Context().Deadline()
						capturedAt = time.Now()
						return &http.Response{
							StatusCode: http.StatusOK,
							Header:     make(http.Header),
							Body:       io.NopCloser(strings.NewReader("")),
						}, nil
					}),
				}

				before := time.Now()
				result := runManifest(t, runner, tt.manifest)
				assert.Empty(t, result.Error)
				assert.True(t, result.Up)
				require.True(t, hasDL)
				assert.False(t, deadline.Before(before.Add(tt.expected)), "deadline %v was earlier than %v", deadline, before.Add(tt.expected))
				assert.False(t, deadline.After(capturedAt.Add(tt.expected)), "deadline %v was later than %v", deadline, capturedAt.Add(tt.expected))
			})
		}
	})

	t.Run("blocks SSRF metadata hosts without sending or retrying", func(t *testing.T) {
		t.Parallel()
		ssrfHosts := []string{
			"http://169.254.169.254/latest/meta-data/",
			"HTTP://169.254.169.254/latest/meta-data/",
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
				t.Parallel()
				var requests atomic.Int32
				runner := Runner{
					Jitter: zeroJitter,
					Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
						requests.Add(1)
						return nil, fmt.Errorf("unexpected request to %s", r.URL)
					}),
				}

				result := runManifest(t, runner, fmt.Sprintf("targets:\n  - name: test\n    url: %q\n    retries: 2\n", host))
				assert.Equal(t, errSSRFBlocked.Error(), result.Error)
				assert.False(t, result.Up)
				assert.Equal(t, int32(0), requests.Load())
			})
		}
	})

	t.Run("captures status/body/headers/latency basics", func(t *testing.T) {
		t.Parallel()
		expectedBody := "Hello, Prober!"
		expectedHeaderKey := "X-Response-Header"
		expectedHeaderValue := "response-value"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(expectedHeaderKey, expectedHeaderValue)
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte(expectedBody))
		}))
		defer server.Close()

		result := runManifest(t, defaultRunner, fmt.Sprintf("targets:\n  - name: test\n    url: %s\n", server.URL))

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
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		result := runManifest(t, defaultRunner, fmt.Sprintf(`targets:
  - name: test
    url: %s
    assertions:
      - status == 404
`, server.URL))

		assert.Empty(t, result.Error)
		assert.False(t, result.Up)
		assert.Equal(t, "status == 404", result.FailedAssertion)
	})

	t.Run("handles timeout/cancel path", func(t *testing.T) {
		t.Parallel()
		runner := Runner{
			Jitter: zeroJitter,
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				<-r.Context().Done()
				return nil, r.Context().Err()
			}),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		plan, err := Prepare(writeManifestFile(t, "targets:\n  - name: slow\n    url: http://example.com\n    timeout: 10\n"))
		require.NoError(t, err)
		summary := runner.Execute(ctx, plan, nil)
		require.Len(t, summary.Results, 1)
		result := summary.Results[0]

		assert.NotEmpty(t, result.Error)
		assert.Contains(t, result.Error, "context deadline exceeded")
		assert.False(t, result.Up)
	})

	t.Run("handles body read errors", func(t *testing.T) {
		t.Parallel()
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

		result := runManifest(t, runner, "targets:\n  - name: test\n    url: http://example.com\n")
		assert.False(t, result.Up)
		assert.Contains(t, result.Error, "connection reset by peer")
	})

	t.Run("handles request body", func(t *testing.T) {
		t.Parallel()
		expectedBody := "request body content"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqBody, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Equal(t, expectedBody, string(reqBody))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		result := runManifest(t, defaultRunner, fmt.Sprintf(`targets:
  - name: test
    url: %s
    method: POST
    body: %q
`, server.URL, expectedBody))
		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
	})

	t.Run("handles SSL information capture", func(t *testing.T) {
		t.Parallel()
		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer tlsServer.Close()

		runner := Runner{
			Jitter:    zeroJitter,
			Transport: tlsServer.Client().Transport,
		}
		result := runManifest(t, runner, fmt.Sprintf("targets:\n  - name: test\n    url: %s\n", tlsServer.URL))

		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
		assert.GreaterOrEqual(t, result.SSLDaysLeft, 0)
		assert.NotEmpty(t, result.SSLIssuer)
		assert.NotEmpty(t, result.SSLSubject)
	})

	t.Run("handles SSLSkipVerify for insecure client", func(t *testing.T) {
		t.Parallel()
		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer tlsServer.Close()

		result := runManifest(t, defaultRunner, fmt.Sprintf(`targets:
  - name: test
    url: %s
    ssl_skip_verify: true
`, tlsServer.URL))

		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
		assert.GreaterOrEqual(t, result.SSLDaysLeft, 0)
		assert.NotEmpty(t, result.SSLIssuer)
		assert.NotEmpty(t, result.SSLSubject)
	})

	t.Run("strips port from Host header for TLS SNI", func(t *testing.T) {
		t.Parallel()
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
		result := runManifest(t, runner, fmt.Sprintf(`targets:
  - name: test
    url: %s
    headers:
      Host: example.com:8443
`, tlsServer.URL))

		assert.Empty(t, result.Error)
		assert.True(t, result.Up)
		assert.Equal(t, "example.com:8443", gotHost)
		assert.Equal(t, "example.com", gotSNI)
	})

	t.Run("retries until success", func(t *testing.T) {
		t.Parallel()
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

		result := runManifest(t, runner, `targets:
  - name: test
    url: http://example.com
    retries: 1
    assertions:
      - status == 200
`)
		assert.True(t, result.Up)
		assert.Equal(t, int32(2), attempts.Load())
	})

	t.Run("returns failure after exhausting retries", func(t *testing.T) {
		t.Parallel()
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

		result := runManifest(t, runner, `targets:
  - name: test
    url: http://example.com
    retries: 2
    assertions:
      - status == 200
`)
		assert.False(t, result.Up)
		assert.Equal(t, "status == 200", result.FailedAssertion)
		assert.Equal(t, int32(3), attempts.Load())
	})
}

func TestRunnerConcurrencyAndCancellation(t *testing.T) {
	t.Parallel()

	t.Run("bounds concurrent probes to MaxConcurrency", func(t *testing.T) {
		t.Parallel()
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

		var b strings.Builder
		b.WriteString("targets:\n")
		for i := range 6 {
			fmt.Fprintf(&b, "  - name: t-%d\n    url: http://example.com\n", i)
		}
		plan, err := Prepare(writeManifestFile(t, b.String()))
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
		t.Parallel()
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

		plan, err := Prepare(writeManifestFile(t, `targets:
  - name: a
    url: http://example.com
  - name: b
    url: http://example.com
    disabled: true
`))
		require.NoError(t, err)

		summary := runner.Execute(ctx, plan, nil)
		assert.Equal(t, int32(0), requests.Load())
		assert.Equal(t, 2, summary.Total)
		assert.Equal(t, 0, summary.Passed)
		assert.Equal(t, 1, summary.Failed)
		assert.Equal(t, 1, summary.Skipped)
		require.Len(t, summary.Results, 1)
		assert.Contains(t, summary.Results[0].Error, "context canceled")
		assert.Equal(t, http.MethodGet, summary.Results[0].Method)
	})

	t.Run("uses injected clock for timestamps and durations", func(t *testing.T) {
		t.Parallel()
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

		plan, err := Prepare(writeManifestFile(t, "targets:\n  - name: a\n    url: http://example.com\n"))
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

	plan, err := Prepare(writeManifestFile(t, `targets:
  - name: pass
    url: http://example.com
    assertions:
      - status == 200
  - name: fail
    url: http://example.com
    assertions:
      - status == 500
  - name: skip
    url: http://example.com
    disabled: true
`))
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
	require.Len(t, summary.SkippedResults, 1)
	assert.Equal(t, http.MethodGet, summary.SkippedResults[0].Method)
	assert.ElementsMatch(t, []string{"pass", "fail"}, published)
	assert.GreaterOrEqual(t, summary.Duration, time.Duration(0))
}

func TestExecuteRespectsCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	plan, err := Prepare(writeManifestFile(t, "targets:\n  - name: a\n    url: http://example.com\n"))
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
