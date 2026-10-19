package output

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeContext(t *testing.T) {
	ctx := NewRuntimeContext()

	str := "value"
	flag := true
	items := []string{"stdout", "file"}
	ctx.Set("string", &str)
	ctx.Set("bool", &flag)
	ctx.Set(string(SelectionKey), &items)

	assert.Equal(t, "value", ctx.GetString("string"))
	assert.True(t, ctx.GetBool("bool"))
	assert.Equal(t, items, ctx.GetStrings(string(SelectionKey)))
	assert.True(t, ctx.OutputSelected(OutputStdout))
	assert.False(t, ctx.OutputSelected(OutputStatsdGCP))
	assert.Equal(t, "", ctx.GetString("missing"))
	assert.False(t, ctx.GetBool("missing"))
	assert.Nil(t, ctx.GetStrings("missing"))
}

func TestParseSelections(t *testing.T) {
	t.Run("deduplicates while preserving order", func(t *testing.T) {
		selected, err := ParseSelections([]string{"file", "stdout", "file"})
		require.NoError(t, err)
		assert.Equal(t, []Name{OutputFile, OutputStdout}, selected)
	})

	t.Run("trims whitespace", func(t *testing.T) {
		selected, err := ParseSelections([]string{" file ", " stdout "})
		require.NoError(t, err)
		assert.Equal(t, []Name{OutputFile, OutputStdout}, selected)
	})

	t.Run("rejects unknown output", func(t *testing.T) {
		_, err := ParseSelections([]string{"unknown"})
		assert.ErrorContains(t, err, "invalid output")
		assert.ErrorContains(t, err, string(OutputStdout))
	})
}

func TestFilePublisherFactory(t *testing.T) {
	hook := Registry[string(OutputFile)]

	t.Run("returns nil when not selected", func(t *testing.T) {
		ctx := NewRuntimeContext()
		ctx.Set(string(OutputFile), stringPtr("/tmp/out.log"))
		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.Nil(t, pub)
	})

	t.Run("errors when selected without config", func(t *testing.T) {
		selected := []string{string(OutputFile)}
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)

		pub, err := hook.Factory(ctx)
		assert.Nil(t, pub)
		assert.ErrorContains(t, err, "--file is required")
	})

	t.Run("opens file when configured", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.log")
		selected := []string{string(OutputFile)}
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)
		ctx.Set(string(OutputFile), stringPtr(path))

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		require.NotNil(t, pub)
		assert.NoError(t, pub.Close())
	})

	t.Run("opens file when default config is present", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), DefaultFilePath)
		selected := []string{string(OutputFile)}
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)
		ctx.Set(string(OutputFile), stringPtr(path))

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		require.NotNil(t, pub)
		assert.NoError(t, pub.Close())
	})
}

func TestFilePublisherPublishAndClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.log")
	f, err := os.Create(path)
	require.NoError(t, err)

	pub := &FilePublisher{file: f}
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	res := Result{
		URL:       "http://example.com",
		Up:        true,
		Status:    200,
		LatencyMS: 42,
		Tags:      map[string]string{"env": "test"},
		Error:     "",
		Timestamp: ts,
	}

	require.NoError(t, pub.Publish(res))
	require.NoError(t, pub.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var got fileLogSchema
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(data), &got))
	assert.Equal(t, ts.Format(time.RFC3339), got.Timestamp)
	assert.Equal(t, res.URL, got.URL)
	assert.Equal(t, res.Up, got.Up)
	assert.Equal(t, res.Status, got.Status)
	assert.Equal(t, res.LatencyMS, got.LatencyMS)
	assert.Equal(t, res.Tags, got.Tags)
}

func TestStdoutPublisherFactory(t *testing.T) {
	hook := Registry[string(OutputStdout)]

	t.Run("returns publisher when no outputs are selected", func(t *testing.T) {
		ctx := NewRuntimeContext()

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.IsType(t, &StdoutPublisher{}, pub)
		assert.NoError(t, pub.Close())
	})

	t.Run("returns nil when another output is selected", func(t *testing.T) {
		selected := []string{string(OutputFile)}
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.Nil(t, pub)
	})

	t.Run("returns publisher when explicitly selected", func(t *testing.T) {
		selected := []string{string(OutputStdout)}
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.IsType(t, &StdoutPublisher{}, pub)
		assert.NoError(t, pub.Close())
	})
}

func TestSummaryOutputFactory(t *testing.T) {
	hook := Registry[string(OutputSummary)]

	t.Run("returns nil when disabled", func(t *testing.T) {
		ctx := NewRuntimeContext()

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.Nil(t, pub)
	})

	t.Run("returns nil when another output is selected", func(t *testing.T) {
		selected := []string{string(OutputFile)}
		var buf bytes.Buffer
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)
		ctx.Set(StdoutWriterKey, &buf)

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.Nil(t, pub)
	})

	t.Run("returns publisher when explicitly selected", func(t *testing.T) {
		selected := []string{string(OutputSummary)}
		var buf bytes.Buffer
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)
		ctx.Set(StdoutWriterKey, &buf)

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.IsType(t, &SummaryOutput{}, pub)
	})
}

func TestSummaryOutputPublishSummary(t *testing.T) {
	t.Run("prints summary and failures", func(t *testing.T) {
		var buf bytes.Buffer
		pub := &SummaryOutput{writer: &buf}

		err := pub.PublishSummary(Summary{
			Total:    2,
			Passed:   1,
			Failed:   1,
			Skipped:  0,
			Duration: 1234 * time.Millisecond,
			Results: []Result{
				{URL: "https://ok.example.com", Up: true, Status: 200},
				{URL: "https://bad.example.com", Up: false, Status: 500, FailedAssertion: "status == 200"},
			},
		})
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "Summary: total=2 passed=1 failed=1 skipped=0 duration=1.234s")
		assert.Contains(t, buf.String(), "FAILED https://bad.example.com status=500 reason=status == 200")
		assert.NotContains(t, buf.String(), "ok.example.com")
	})

	t.Run("returns writer errors", func(t *testing.T) {
		pub := &SummaryOutput{writer: failingWriter{}}

		err := pub.PublishSummary(Summary{Duration: time.Second})
		assert.ErrorContains(t, err, "boom")
	})
}

func TestJUnitOutputFactory(t *testing.T) {
	hook := Registry[string(OutputJunit)]

	t.Run("returns nil when not selected", func(t *testing.T) {
		ctx := NewRuntimeContext()
		ctx.Set(string(OutputJunit), stringPtr("/tmp/report.xml"))

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.Nil(t, pub)
	})

	t.Run("errors when selected without config", func(t *testing.T) {
		selected := []string{string(OutputJunit)}
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)

		pub, err := hook.Factory(ctx)
		assert.Nil(t, pub)
		assert.ErrorContains(t, err, "--junit is required")
	})

	t.Run("returns publisher when configured", func(t *testing.T) {
		selected := []string{string(OutputJunit)}
		path := filepath.Join(t.TempDir(), "report.xml")
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)
		ctx.Set(string(OutputJunit), stringPtr(path))

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.IsType(t, &JUnitOutput{}, pub)
	})

	t.Run("returns publisher when default config is present", func(t *testing.T) {
		selected := []string{string(OutputJunit)}
		path := filepath.Join(t.TempDir(), DefaultJUnitPath)
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)
		ctx.Set(string(OutputJunit), stringPtr(path))

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.IsType(t, &JUnitOutput{}, pub)
	})
}

func TestJUnitOutputPublishSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.xml")
	pub := &JUnitOutput{path: path}

	err := pub.PublishSummary(Summary{
		Total:    2,
		Passed:   1,
		Failed:   1,
		Skipped:  1,
		Duration: 1500 * time.Millisecond,
		Results: []Result{
			{Name: "homepage", URL: "https://example.com", Up: true, Status: 200, LatencyMS: 15, Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Body: "ok"},
			{Name: "api", URL: "https://api.example.com", Up: false, Status: 500, LatencyMS: 35, FailedAssertion: "status == 200", Timestamp: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), Body: "failure"},
		},
		SkippedResults: []Result{{Name: "disabled-target", URL: "https://disabled.example.com", Timestamp: time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC)}},
	})
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), xml.Header)

	var report junitTestSuite
	require.NoError(t, xml.Unmarshal(data, &report))
	assert.Equal(t, "prober", report.Name)
	assert.Equal(t, 3, report.Tests)
	assert.Equal(t, 1, report.Failures)
	assert.Equal(t, 1, report.Skipped)
	assert.Equal(t, "1.500", report.Time)
	assert.Equal(t, "2026-01-02T03:04:04Z", report.Timestamp)
	require.Len(t, report.TestCases, 3)
	assert.Equal(t, "disabled-target", report.TestCases[0].Name)
	assert.NotNil(t, report.TestCases[0].Skipped)
	assert.Equal(t, "homepage", report.TestCases[1].Name)
	assert.Nil(t, report.TestCases[1].Failure)
	assert.Equal(t, "ok", report.TestCases[1].SystemOut)
	assert.Equal(t, "api", report.TestCases[2].Name)
	if assert.NotNil(t, report.TestCases[2].Failure) {
		assert.Equal(t, "status == 200", report.TestCases[2].Failure.Message)
		assert.Equal(t, "assertion", report.TestCases[2].Failure.Type)
	}
	assert.Equal(t, "failure", report.TestCases[2].SystemOut)
}

func TestStatsdPublisherFactories(t *testing.T) {
	t.Run("datadog returns nil when not selected", func(t *testing.T) {
		hook := Registry[string(OutputStatsdDatadog)]
		ctx := NewRuntimeContext()
		ctx.Set(string(OutputStatsdDatadog), stringPtr("127.0.0.1:8125"))

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.Nil(t, pub)
	})

	t.Run("datadog errors when selected without config", func(t *testing.T) {
		hook := Registry[string(OutputStatsdDatadog)]
		selected := []string{string(OutputStatsdDatadog)}
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)

		pub, err := hook.Factory(ctx)
		assert.Nil(t, pub)
		assert.ErrorContains(t, err, "--statsd-datadog is required")
	})

	t.Run("datadog returns publisher when default config is present", func(t *testing.T) {
		hook := Registry[string(OutputStatsdDatadog)]
		selected := []string{string(OutputStatsdDatadog)}
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)
		ctx.Set(string(OutputStatsdDatadog), stringPtr(DefaultStatsDAddr))

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.IsType(t, &DatadogStatsDPublisher{}, pub)
	})

	t.Run("gcp returns nil when not selected", func(t *testing.T) {
		hook := Registry[string(OutputStatsdGCP)]
		ctx := NewRuntimeContext()
		ctx.Set(string(OutputStatsdGCP), stringPtr("127.0.0.1:8125"))

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.Nil(t, pub)
	})

	t.Run("gcp errors when selected without config", func(t *testing.T) {
		hook := Registry[string(OutputStatsdGCP)]
		selected := []string{string(OutputStatsdGCP)}
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)

		pub, err := hook.Factory(ctx)
		assert.Nil(t, pub)
		assert.ErrorContains(t, err, "--statsd-gcp is required")
	})

	t.Run("gcp returns publisher when default config is present", func(t *testing.T) {
		hook := Registry[string(OutputStatsdGCP)]
		selected := []string{string(OutputStatsdGCP)}
		ctx := NewRuntimeContext()
		ctx.Set(string(SelectionKey), &selected)
		ctx.Set(string(OutputStatsdGCP), stringPtr(DefaultStatsDAddr))

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.IsType(t, &GCPStatsDPublisher{}, pub)
	})
}

func TestStatsdPublishersSendGauges(t *testing.T) {
	res := Result{URL: "https://example.com", Up: true, Status: 200, LatencyMS: 42, Tags: map[string]string{"environment": "prod", "team": "web"}}

	t.Run("datadog", func(t *testing.T) {
		conn, addr := listenUDP(t)
		pub := newStatsdPublisher(t, OutputStatsdDatadog, addr)

		require.NoError(t, pub.Publish(res))
		require.NoError(t, pub.Close())

		got := readDatagrams(t, conn)
		assert.Contains(t, got, "custom_monitoring.uptime_prober.status:1|g")
		assert.Contains(t, got, "custom_monitoring.uptime_prober.latency_ms:42|g")
	})

	t.Run("gcp", func(t *testing.T) {
		conn, addr := listenUDP(t)
		pub := newStatsdPublisher(t, OutputStatsdGCP, addr)

		require.NoError(t, pub.Publish(res))
		require.NoError(t, pub.Close())

		got := readDatagrams(t, conn)
		assert.Contains(t, got, "uptime_prober.status.env_prod.team_web:1|g")
		assert.Contains(t, got, "uptime_prober.latency_ms.env_prod.team_web:42|g")
	})
}

func TestStatsdPublishersPublishAfterCloseFails(t *testing.T) {
	for _, name := range []Name{OutputStatsdDatadog, OutputStatsdGCP} {
		t.Run(string(name), func(t *testing.T) {
			_, addr := listenUDP(t)
			pub := newStatsdPublisher(t, name, addr)

			require.NoError(t, pub.Close())
			assert.Error(t, pub.Publish(Result{URL: "https://example.com", Up: true}))
		})
	}
}

func TestStatsdPublishersReportSenderErrors(t *testing.T) {
	for _, name := range []Name{OutputStatsdDatadog, OutputStatsdGCP} {
		t.Run(string(name), func(t *testing.T) {
			_, addr := listenUDP(t)
			pub := newStatsdPublisher(t, name, addr)

			res := Result{URL: "https://example.com", Up: true}
			refused, full := errors.New("connection refused"), errors.New("queue full")
			sender := statsdSenderOf(t, pub)
			sender.recordError(refused)
			sender.recordError(full)

			err := pub.Publish(res)
			assert.ErrorIs(t, err, refused)
			assert.ErrorIs(t, err, full)
			assert.NoError(t, pub.Publish(res), "errors are returned once")

			sender.recordError(errors.New("write failed"))
			assert.ErrorContains(t, pub.Close(), "write failed")
		})
	}
}

func TestStatsdPublishersCloseReportsBackgroundSendFailure(t *testing.T) {
	for _, name := range []Name{OutputStatsdDatadog, OutputStatsdGCP} {
		t.Run(string(name), func(t *testing.T) {
			// Nothing listens on this socket, so every background write fails.
			addr := "unix://" + filepath.Join(t.TempDir(), "missing.sock")
			pub := newStatsdPublisher(t, name, addr)

			// The aggregator may flush before Close, so the failure can
			// surface from either call.
			err := errors.Join(pub.Publish(Result{URL: "https://example.com", Up: true}), pub.Close())
			assert.ErrorContains(t, err, "missing.sock")
		})
	}
}

func TestStatsdPublisherFactoriesRejectBadAddress(t *testing.T) {
	for _, name := range []Name{OutputStatsdDatadog, OutputStatsdGCP} {
		t.Run(string(name), func(t *testing.T) {
			selected := []string{string(name)}
			ctx := NewRuntimeContext()
			ctx.Set(string(SelectionKey), &selected)
			ctx.Set(string(name), stringPtr("127.0.0.1:not-a-port"))

			pub, err := Registry[string(name)].Factory(ctx)
			assert.Error(t, err)
			assert.Nil(t, pub)
		})
	}
}

func TestJUnitOutputWriteFailureReportedOnce(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "regular-file")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o644))
	pub := &JUnitOutput{path: filepath.Join(parent, "report.xml")}

	assert.Error(t, pub.PublishSummary(Summary{Duration: time.Second}))
	assert.NoError(t, pub.Close())

	// The report path is a directory, so the final write fails.
	pub = &JUnitOutput{path: t.TempDir()}
	assert.Error(t, pub.PublishSummary(Summary{Duration: time.Second}))
	assert.NoError(t, pub.Close())
}

func TestStdoutPublisherWritesToRuntimeWriter(t *testing.T) {
	var buf bytes.Buffer
	selected := []string{string(OutputStdout)}
	ctx := NewRuntimeContext()
	ctx.Set(string(SelectionKey), &selected)
	ctx.Set(StdoutWriterKey, &buf)

	pub, err := Registry[string(OutputStdout)].Factory(ctx)
	require.NoError(t, err)
	require.NoError(t, pub.Publish(Result{URL: "http://example.com", Up: true, Status: 200}))
	require.NoError(t, pub.Close())

	var line map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line))
	assert.Equal(t, "http://example.com", line["url"])
	assert.Equal(t, "Uptime check passed", line["msg"])
}

func TestStdoutPublisherPublish(t *testing.T) {
	var buf bytes.Buffer
	pub := &StdoutPublisher{logger: slog.New(slog.NewJSONHandler(&buf, nil))}

	require.NoError(t, pub.Publish(Result{URL: "http://example.com", Up: true, Status: 200, LatencyMS: 10, Tags: map[string]string{"service": "web"}}))
	require.NoError(t, pub.Publish(Result{URL: "http://example.com/down", Up: false, Status: 500, LatencyMS: 20, Error: "boom", FailedAssertion: "status == 200"}))

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.Len(t, lines, 2)

	var first map[string]any
	var second map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &first))
	require.NoError(t, json.Unmarshal(lines[1], &second))

	assert.Equal(t, "Uptime check passed", first["msg"])
	assert.Equal(t, "INFO", first["level"])
	assert.Equal(t, "web", first["service"])
	assert.Equal(t, "Uptime check failed", second["msg"])
	assert.Equal(t, "ERROR", second["level"])
	assert.Equal(t, "boom", second["error"])
	assert.Equal(t, "status == 200", second["failed_assertion"])
	assert.NoError(t, pub.Close())
}

func listenUDP(t *testing.T) (net.PacketConn, string) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn, conn.LocalAddr().String()
}

func readDatagrams(t *testing.T, conn net.PacketConn) string {
	t.Helper()
	var got bytes.Buffer
	buf := make([]byte, 65535)
	for {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return got.String()
		}
		got.Write(buf[:n])
		got.WriteByte('\n')
	}
}

func newStatsdPublisher(t *testing.T, name Name, addr string) Publisher {
	t.Helper()
	selected := []string{string(name)}
	ctx := NewRuntimeContext()
	ctx.Set(string(SelectionKey), &selected)
	ctx.Set(string(name), stringPtr(addr))

	pub, err := Registry[string(name)].Factory(ctx)
	require.NoError(t, err)
	require.NotNil(t, pub)
	return pub
}

func statsdSenderOf(t *testing.T, pub Publisher) *statsdSender {
	t.Helper()
	switch p := pub.(type) {
	case *DatadogStatsDPublisher:
		return p.sender
	case *GCPStatsDPublisher:
		return p.sender
	}
	t.Fatalf("not a statsd publisher: %T", pub)
	return nil
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func stringPtr(v string) *string { return &v }
