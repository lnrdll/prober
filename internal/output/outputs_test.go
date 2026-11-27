package output

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lnrdll/prober/internal/prober"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCatalogNamesAndSettingDescriptors(t *testing.T) {
	t.Parallel()

	t.Run("names match catalog order and return an isolated slice", func(t *testing.T) {
		want := []Name{"summary", "junit", "stdout", "file", "statsd-datadog", "statsd-gcp"}
		got := Names()
		assert.Equal(t, want, got)

		got[0] = "mutated"
		assert.Equal(t, want, Names())
	})

	t.Run("setting descriptors match configurable catalog order and return an isolated slice", func(t *testing.T) {
		want := []SettingDescriptor{
			{
				Name:        "junit",
				Default:     "prober.xml",
				Description: "Write a JUnit XML report to a file",
			},
			{
				Name:        "file",
				Default:     "prober.log",
				Description: "Append structured JSON log lines to a file",
			},
			{
				Name:        "statsd-datadog",
				Default:     "127.0.0.1:8125",
				Description: "UDP network address location pointing to local Datadog daemon",
			},
			{
				Name:        "statsd-gcp",
				Default:     "127.0.0.1:8125",
				Description: "UDP network address location pointing to local GCP Ops Agent",
			},
		}
		got := SettingDescriptors()
		require.Equal(t, want, got)

		got[0].Default = "mutated.xml"
		assert.Equal(t, want, SettingDescriptors())
	})
}

func TestOpenUsesStdoutWhenNothingIsSelected(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	outputs, err := Open(Config{Writer: &buf})
	require.NoError(t, err)

	outputs.Publish(prober.Result{URL: "http://example.com", Up: true, Status: 200})
	require.NoError(t, outputs.Finish(prober.Summary{Total: 1, Passed: 1}))

	var line map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line))
	assert.Equal(t, "http://example.com", line["url"])
	assert.Equal(t, "Uptime check passed", line["msg"])
	assert.NotContains(t, buf.String(), "Summary:")
}

func TestOpenDeduplicatesSelections(t *testing.T) {
	t.Parallel()

	t.Run("deduplicates single output with surrounding whitespace", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		outputs, err := Open(Config{Selections: []string{"stdout", " stdout "}, Writer: &buf})
		require.NoError(t, err)

		outputs.Publish(prober.Result{URL: "http://example.com", Up: true, Status: 200})
		require.NoError(t, outputs.Finish(prober.Summary{}))

		assert.Len(t, bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")), 1)
	})

	t.Run("deduplicates multiple outputs with surrounding whitespace", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		path := filepath.Join(t.TempDir(), "out.log")
		outputs, err := Open(Config{
			Selections: []string{" file ", "stdout", "file", " stdout "},
			Writer:     &buf,
			Settings:   map[Name]Setting{"file": {Value: path, Set: true}},
		})
		require.NoError(t, err)

		outputs.Publish(prober.Result{URL: "http://example.com", Up: true, Status: 200})
		require.NoError(t, outputs.Finish(prober.Summary{}))

		assert.Len(t, bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")), 1)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Len(t, bytes.Split(bytes.TrimSpace(data), []byte("\n")), 1)
	})

	t.Run("preserves selection order when deduplicating multiple outputs", func(t *testing.T) {
		t.Parallel()
		junitDir := t.TempDir()

		outputs, err := Open(Config{
			Selections: []string{" summary ", "junit", "summary", " junit "},
			Writer:     failingWriter{},
			Settings:   map[Name]Setting{"junit": {Value: junitDir, Set: true}},
		})
		require.NoError(t, err)
		err = outputs.Finish(prober.Summary{Duration: time.Second})
		require.Error(t, err)
		summaryIdx := strings.Index(err.Error(), "summary: publish summary failed: boom")
		junitIdx := strings.Index(err.Error(), "junit: publish summary failed:")
		assert.Greater(t, summaryIdx, -1, err.Error())
		assert.Greater(t, junitIdx, summaryIdx, err.Error())
		assert.NotContains(t, err.Error(), "failed 2 times")

		outputs, err = Open(Config{
			Selections: []string{" junit ", "summary", "junit", " summary "},
			Writer:     failingWriter{},
			Settings:   map[Name]Setting{"junit": {Value: junitDir, Set: true}},
		})
		require.NoError(t, err)
		err = outputs.Finish(prober.Summary{Duration: time.Second})
		require.Error(t, err)
		summaryIdx = strings.Index(err.Error(), "summary: publish summary failed: boom")
		junitIdx = strings.Index(err.Error(), "junit: publish summary failed:")
		assert.Greater(t, junitIdx, -1, err.Error())
		assert.Greater(t, summaryIdx, junitIdx, err.Error())
		assert.NotContains(t, err.Error(), "failed 2 times")
	})
}

func TestOutputsWriteStdoutAndSummaryToWriter(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	outputs, err := Open(Config{Selections: []string{"stdout", "summary"}, Writer: &buf})
	require.NoError(t, err)

	outputs.Publish(prober.Result{URL: "https://bad.example.com", Up: false, Status: 500, Error: "boom"})
	err = outputs.Finish(prober.Summary{
		Total:    1,
		Failed:   1,
		Duration: 1234 * time.Millisecond,
		Results:  []prober.Result{{URL: "https://bad.example.com", Up: false, Status: 500, Error: "boom"}},
	})
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 3)
	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	assert.Equal(t, "Uptime check failed", first["msg"])
	assert.Equal(t, "boom", first["error"])
	assert.Equal(t, "Summary: total=1 passed=0 failed=1 skipped=0 duration=1.234s", lines[1])
	assert.Equal(t, "FAILED https://bad.example.com status=500 reason=boom", lines[2])
}

func TestOutputsWriteStdoutAttributes(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	outputs, err := Open(Config{Selections: []string{"stdout"}, Writer: &buf})
	require.NoError(t, err)

	outputs.Publish(prober.Result{URL: "http://example.com", Up: true, Status: 200, LatencyMS: 10, Tags: map[string]string{"service": "web"}})
	outputs.Publish(prober.Result{URL: "http://example.com/down", Up: false, Status: 500, LatencyMS: 20, Error: "boom", FailedAssertion: "status == 200"})
	require.NoError(t, outputs.Finish(prober.Summary{}))

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.Len(t, lines, 2)

	var first map[string]any
	var second map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &first))
	require.NoError(t, json.Unmarshal(lines[1], &second))

	assert.Equal(t, "Uptime check passed", first["msg"])
	assert.Equal(t, "INFO", first["level"])
	assert.Equal(t, "http://example.com", first["url"])
	assert.Equal(t, true, first["up"])
	assert.Equal(t, float64(200), first["status"])
	assert.Equal(t, float64(10), first["latency_ms"])
	assert.Equal(t, "web", first["service"])
	assert.NotContains(t, first, "error")
	assert.NotContains(t, first, "failed_assertion")

	assert.Equal(t, "Uptime check failed", second["msg"])
	assert.Equal(t, "ERROR", second["level"])
	assert.Equal(t, "http://example.com/down", second["url"])
	assert.Equal(t, false, second["up"])
	assert.Equal(t, float64(500), second["status"])
	assert.Equal(t, float64(20), second["latency_ms"])
	assert.Equal(t, "boom", second["error"])
	assert.Equal(t, "status == 200", second["failed_assertion"])
}

func TestOutputsWriteSummary(t *testing.T) {
	t.Parallel()

	t.Run("omits passing targets from failed lines", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		outputs, err := Open(Config{Selections: []string{"summary"}, Writer: &buf})
		require.NoError(t, err)

		err = outputs.Finish(prober.Summary{
			Total:    2,
			Passed:   1,
			Failed:   1,
			Skipped:  0,
			Duration: 1234 * time.Millisecond,
			Results: []prober.Result{
				{URL: "https://ok.example.com", Up: true, Status: 200},
				{URL: "https://bad.example.com", Up: false, Status: 500, FailedAssertion: "status == 200"},
			},
		})
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "Summary: total=2 passed=1 failed=1 skipped=0 duration=1.234s")
		assert.Contains(t, buf.String(), "FAILED https://bad.example.com status=500 reason=status == 200")
		assert.NotContains(t, buf.String(), "ok.example.com")
	})

	t.Run("propagates writer errors", func(t *testing.T) {
		t.Parallel()
		outputs, err := Open(Config{Selections: []string{"summary"}, Writer: failingWriter{}})
		require.NoError(t, err)

		err = outputs.Finish(prober.Summary{Duration: time.Second})
		assert.EqualError(t, err, "output errors:\n  summary: publish summary failed: boom")
	})
}

func TestOutputsAppendFileLog(t *testing.T) {
	t.Parallel()

	t.Run("writes passing result without failed_assertion or error keys", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "out.log")
		outputs, err := Open(Config{
			Selections: []string{"file"},
			Settings:   map[Name]Setting{"file": {Value: path, Set: true}},
		})
		require.NoError(t, err)

		ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		outputs.Publish(prober.Result{URL: "http://example.com", Up: true, Status: 200, LatencyMS: 42, Tags: map[string]string{"env": "test"}, Timestamp: ts})
		require.NoError(t, outputs.Finish(prober.Summary{}))

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var got fileLogSchema
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(data), &got))
		assert.Equal(t, fileLogSchema{
			Timestamp: "2026-01-02T03:04:05Z",
			URL:       "http://example.com",
			Up:        true,
			Status:    200,
			LatencyMS: 42,
			Tags:      map[string]string{"env": "test"},
		}, got)
		assert.NotContains(t, string(data), `"failed_assertion"`)
		assert.NotContains(t, string(data), `"error"`)
	})

	t.Run("records failed assertion without error key", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "out.log")
		outputs, err := Open(Config{
			Selections: []string{"file"},
			Settings:   map[Name]Setting{"file": {Value: path, Set: true}},
		})
		require.NoError(t, err)

		ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		outputs.Publish(prober.Result{
			URL:             "http://example.com",
			Up:              false,
			Status:          500,
			LatencyMS:       42,
			FailedAssertion: "status == 200",
			Timestamp:       ts,
		})
		require.NoError(t, outputs.Finish(prober.Summary{}))

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var got map[string]any
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(data), &got))
		assert.Equal(t, false, got["up"])
		assert.Equal(t, "status == 200", got["failed_assertion"])
		assert.NotContains(t, got, "error")
	})
}

func TestOutputsWriteJUnitReport(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "report.xml")
	outputs, err := Open(Config{
		Selections: []string{"junit"},
		Settings:   map[Name]Setting{"junit": {Value: path, Set: true}},
	})
	require.NoError(t, err)

	passing := prober.Result{Name: "homepage", URL: "https://example.com", Up: true, Status: 200, LatencyMS: 15, Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Body: "ok"}
	failedAssertion := prober.Result{Name: "api", URL: "https://api.example.com", Up: false, Status: 500, LatencyMS: 35, FailedAssertion: "status == 200", Timestamp: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), Body: "failure"}
	failedError := prober.Result{Name: "backend", URL: "https://backend.example.com", Up: false, Status: 0, LatencyMS: 50, Error: "connection refused", Timestamp: time.Date(2026, 1, 2, 3, 4, 7, 0, time.UTC)}
	skipped := prober.Result{Name: "disabled-target", URL: "https://disabled.example.com", Timestamp: time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC)}

	outputs.Publish(passing)
	outputs.Publish(failedAssertion)
	outputs.Publish(failedError)
	require.NoError(t, outputs.Finish(prober.Summary{
		Total:          3,
		Passed:         1,
		Failed:         2,
		Skipped:        1,
		Duration:       1500 * time.Millisecond,
		Results:        []prober.Result{passing, failedAssertion, failedError},
		SkippedResults: []prober.Result{skipped},
	}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), xml.Header)

	var report junitTestSuite
	require.NoError(t, xml.Unmarshal(data, &report))
	assert.Equal(t, "prober", report.Name)
	assert.Equal(t, 4, report.Tests)
	assert.Equal(t, 2, report.Failures)
	assert.Equal(t, 1, report.Skipped)
	assert.Equal(t, "1.500", report.Time)
	assert.Equal(t, "2026-01-02T03:04:04Z", report.Timestamp)
	require.Len(t, report.TestCases, 4)

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

	assert.Equal(t, "backend", report.TestCases[3].Name)
	if assert.NotNil(t, report.TestCases[3].Failure) {
		assert.Equal(t, "connection refused", report.TestCases[3].Failure.Message)
		assert.Equal(t, "error", report.TestCases[3].Failure.Type)
	}
	assert.Empty(t, report.TestCases[3].SystemOut)
}

func TestOutputsReportJUnitWriteFailureOnce(t *testing.T) {
	t.Parallel()

	t.Run("parent path is a regular file", func(t *testing.T) {
		t.Parallel()
		parent := filepath.Join(t.TempDir(), "regular-file")
		require.NoError(t, os.WriteFile(parent, []byte("x"), 0o644))

		outputs, err := Open(Config{
			Selections: []string{"junit"},
			Settings:   map[Name]Setting{"junit": {Value: filepath.Join(parent, "report.xml"), Set: true}},
		})
		require.NoError(t, err)

		err = outputs.Finish(prober.Summary{Duration: time.Second})
		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "output errors:\n  junit: publish summary failed: "), err.Error())
		assert.NotContains(t, err.Error(), "close failed")
	})

	t.Run("report path is a directory", func(t *testing.T) {
		t.Parallel()
		outputs, err := Open(Config{
			Selections: []string{"junit"},
			Settings:   map[Name]Setting{"junit": {Value: t.TempDir(), Set: true}},
		})
		require.NoError(t, err)

		err = outputs.Finish(prober.Summary{Duration: time.Second})
		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "output errors:\n  junit: publish summary failed: "), err.Error())
		assert.NotContains(t, err.Error(), "close failed")
	})
}

func TestOutputsSendStatsDGauges(t *testing.T) {
	t.Parallel()

	t.Run("datadog emits namespace, tags, and ssl_days_left when positive", func(t *testing.T) {
		t.Parallel()
		conn, addr := listenUDP(t)
		outputs, err := Open(Config{
			Selections: []string{"statsd-datadog"},
			Settings:   map[Name]Setting{"statsd-datadog": {Value: addr, Set: true}},
		})
		require.NoError(t, err)

		res := prober.Result{
			URL:         "https://example.com",
			Up:          true,
			Status:      200,
			LatencyMS:   42,
			SSLDaysLeft: 30,
			Tags:        map[string]string{"environment": "prod", "team": "web"},
		}
		outputs.Publish(res)
		require.NoError(t, outputs.Finish(prober.Summary{}))

		got := readDatagrams(t, conn,
			"custom_monitoring.uptime_prober.status:1|g",
			"custom_monitoring.uptime_prober.latency_ms:42|g",
			"custom_monitoring.uptime_prober.ssl_days_left:30|g",
		)
		metrics := parseStatsDMetrics(t, got)
		wantTags := []string{"url:https://example.com", "environment:prod", "team:web"}

		status, ok := metrics["custom_monitoring.uptime_prober.status"]
		require.True(t, ok, "missing status metric in %q", got)
		assert.Equal(t, "1|g", status.value)
		assert.ElementsMatch(t, wantTags, status.tags)

		latency, ok := metrics["custom_monitoring.uptime_prober.latency_ms"]
		require.True(t, ok, "missing latency_ms metric in %q", got)
		assert.Equal(t, "42|g", latency.value)
		assert.ElementsMatch(t, wantTags, latency.tags)

		ssl, ok := metrics["custom_monitoring.uptime_prober.ssl_days_left"]
		require.True(t, ok, "missing ssl_days_left metric in %q", got)
		assert.Equal(t, "30|g", ssl.value)
		assert.ElementsMatch(t, wantTags, ssl.tags)
	})

	t.Run("datadog emits zero status when down and omits ssl_days_left when non-positive", func(t *testing.T) {
		t.Parallel()
		conn, addr := listenUDP(t)
		outputs, err := Open(Config{
			Selections: []string{"statsd-datadog"},
			Settings:   map[Name]Setting{"statsd-datadog": {Value: addr, Set: true}},
		})
		require.NoError(t, err)

		outputs.Publish(prober.Result{
			URL:         "https://down.example.com",
			Up:          false,
			Status:      503,
			LatencyMS:   15,
			SSLDaysLeft: 0,
		})
		require.NoError(t, outputs.Finish(prober.Summary{}))

		got := readDatagrams(t, conn,
			"custom_monitoring.uptime_prober.status:0|g",
			"custom_monitoring.uptime_prober.latency_ms:15|g",
		)
		metrics := parseStatsDMetrics(t, got)
		wantTags := []string{"url:https://down.example.com"}

		status, ok := metrics["custom_monitoring.uptime_prober.status"]
		require.True(t, ok, "missing status metric in %q", got)
		assert.Equal(t, "0|g", status.value)
		assert.ElementsMatch(t, wantTags, status.tags)

		latency, ok := metrics["custom_monitoring.uptime_prober.latency_ms"]
		require.True(t, ok, "missing latency_ms metric in %q", got)
		assert.Equal(t, "15|g", latency.value)
		assert.ElementsMatch(t, wantTags, latency.tags)

		assert.NotContains(t, got, "ssl_days_left")
	})

	t.Run("gcp appends environment and team suffix, tags only url, and omits ssl_days_left", func(t *testing.T) {
		t.Parallel()
		conn, addr := listenUDP(t)
		outputs, err := Open(Config{
			Selections: []string{"statsd-gcp"},
			Settings:   map[Name]Setting{"statsd-gcp": {Value: addr, Set: true}},
		})
		require.NoError(t, err)

		outputs.Publish(prober.Result{
			URL:         "https://example.com",
			Up:          true,
			Status:      200,
			LatencyMS:   42,
			SSLDaysLeft: 30,
			Tags:        map[string]string{"environment": "prod", "team": "web", "region": "us"},
		})
		require.NoError(t, outputs.Finish(prober.Summary{}))

		got := readDatagrams(t, conn,
			"uptime_prober.status.env_prod.team_web:1|g",
			"uptime_prober.latency_ms.env_prod.team_web:42|g",
		)
		metrics := parseStatsDMetrics(t, got)
		wantTags := []string{"url:https://example.com"}

		status, ok := metrics["uptime_prober.status.env_prod.team_web"]
		require.True(t, ok, "missing status metric in %q", got)
		assert.Equal(t, "1|g", status.value)
		assert.Equal(t, wantTags, status.tags)

		latency, ok := metrics["uptime_prober.latency_ms.env_prod.team_web"]
		require.True(t, ok, "missing latency_ms metric in %q", got)
		assert.Equal(t, "42|g", latency.value)
		assert.Equal(t, wantTags, latency.tags)

		assert.NotContains(t, got, "ssl_days_left")
		assert.NotContains(t, got, "custom_monitoring.")
	})

	t.Run("gcp uses unsuffixed names without both environment and team tags and emits zero status when down", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			label string
			tags  map[string]string
		}{
			{label: "no tags", tags: nil},
			{label: "only environment", tags: map[string]string{"environment": "prod"}},
			{label: "only team", tags: map[string]string{"team": "web"}},
		} {
			t.Run(tc.label, func(t *testing.T) {
				t.Parallel()
				conn, addr := listenUDP(t)
				outputs, err := Open(Config{
					Selections: []string{"statsd-gcp"},
					Settings:   map[Name]Setting{"statsd-gcp": {Value: addr, Set: true}},
				})
				require.NoError(t, err)

				outputs.Publish(prober.Result{
					URL:       "https://down.example.com",
					Up:        false,
					Status:    500,
					LatencyMS: 18,
					Tags:      tc.tags,
				})
				require.NoError(t, outputs.Finish(prober.Summary{}))

				got := readDatagrams(t, conn,
					"uptime_prober.status:0|g",
					"uptime_prober.latency_ms:18|g",
				)
				metrics := parseStatsDMetrics(t, got)
				wantTags := []string{"url:https://down.example.com"}

				status, ok := metrics["uptime_prober.status"]
				require.True(t, ok, "missing status metric in %q", got)
				assert.Equal(t, "0|g", status.value)
				assert.Equal(t, wantTags, status.tags)

				latency, ok := metrics["uptime_prober.latency_ms"]
				require.True(t, ok, "missing latency_ms metric in %q", got)
				assert.Equal(t, "18|g", latency.value)
				assert.Equal(t, wantTags, latency.tags)

				assert.NotContains(t, got, ".env_")
				assert.NotContains(t, got, ".team_")
			})
		}
	})
}

func TestOutputsReportStatsDBackgroundSendFailure(t *testing.T) {
	t.Parallel()
	for _, name := range []Name{outputStatsdDatadog, outputStatsdGCP} {
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()
			// Nothing listens on this socket, so every background write fails.
			addr := "unix://" + filepath.Join(t.TempDir(), "missing.sock")
			outputs, err := Open(Config{
				Selections: []string{string(name)},
				Settings:   map[Name]Setting{name: {Value: addr, Set: true}},
			})
			require.NoError(t, err)

			outputs.Publish(prober.Result{URL: "https://example.com", Up: true})
			err = outputs.Finish(prober.Summary{})
			require.Error(t, err)
			assert.ErrorContains(t, err, string(name))
			assert.ErrorContains(t, err, "missing.sock")
		})
	}
}

func TestOpenAppliesDefaults(t *testing.T) {
	// Serial: t.Chdir mutates process working directory.
	t.Chdir(t.TempDir())

	outputs, err := Open(Config{Selections: []string{"file", "junit"}})
	require.NoError(t, err)
	outputs.Publish(prober.Result{URL: "http://example.com", Up: true})
	require.NoError(t, outputs.Finish(prober.Summary{}))

	assert.FileExists(t, "prober.log")
	assert.FileExists(t, "prober.xml")
}

func TestOpenAppliesStatsDDefaults(t *testing.T) {
	t.Parallel()
	var got []string
	builders := map[Name]constructorFunc{
		outputStatsdDatadog: func(cfg Config) (publisher, error) {
			got = append(got, cfg.Settings[outputStatsdDatadog].Value)
			return &fakeAdapter{}, nil
		},
		outputStatsdGCP: func(cfg Config) (publisher, error) {
			got = append(got, cfg.Settings[outputStatsdGCP].Value)
			return &fakeAdapter{}, nil
		},
	}

	outputs, err := openWith(Config{Selections: []string{"statsd-datadog", "statsd-gcp"}}, builders)
	require.NoError(t, err)
	require.NoError(t, outputs.Finish(prober.Summary{}))

	assert.Equal(t, []string{"127.0.0.1:8125", "127.0.0.1:8125"}, got)
}

func TestOpenDoesNotMutateCallerSettingsMap(t *testing.T) {
	t.Parallel()

	t.Run("leaves empty non-nil settings map untouched when applying defaults", func(t *testing.T) {
		settings := map[Name]Setting{}
		var resolved string
		builders := map[Name]constructorFunc{
			outputStatsdDatadog: func(cfg Config) (publisher, error) {
				resolved = cfg.Settings[outputStatsdDatadog].Value
				return &fakeAdapter{}, nil
			},
		}

		outputs, err := openWith(Config{Selections: []string{"statsd-datadog"}, Settings: settings}, builders)
		require.NoError(t, err)
		require.NoError(t, outputs.Finish(prober.Summary{}))

		assert.Equal(t, "127.0.0.1:8125", resolved)
		assert.Empty(t, settings)
	})

	t.Run("leaves existing unset entry untouched when applying defaults", func(t *testing.T) {
		settings := map[Name]Setting{"statsd-gcp": {}}
		var resolved string
		builders := map[Name]constructorFunc{
			outputStatsdGCP: func(cfg Config) (publisher, error) {
				resolved = cfg.Settings[outputStatsdGCP].Value
				return &fakeAdapter{}, nil
			},
		}

		outputs, err := openWith(Config{Selections: []string{"statsd-gcp"}, Settings: settings}, builders)
		require.NoError(t, err)
		require.NoError(t, outputs.Finish(prober.Summary{}))

		assert.Equal(t, "127.0.0.1:8125", resolved)
		assert.Equal(t, map[Name]Setting{"statsd-gcp": {}}, settings)
	})
}

func TestOpenRejectsBadSelections(t *testing.T) {
	t.Parallel()

	t.Run("unknown output", func(t *testing.T) {
		outputs, err := Open(Config{Selections: []string{"unknown"}})
		assert.Nil(t, outputs)
		assert.EqualError(t, err, `invalid output "unknown" (allowed: summary, junit, stdout, file, statsd-datadog, statsd-gcp)`)
	})

	t.Run("unknown output in settings map", func(t *testing.T) {
		outputs, err := Open(Config{
			Selections: []string{"stdout"},
			Settings:   map[Name]Setting{"bogus": {Value: "x", Set: true}},
		})
		assert.Nil(t, outputs)
		assert.EqualError(t, err, `invalid output "bogus" (allowed: summary, junit, stdout, file, statsd-datadog, statsd-gcp)`)
	})

	t.Run("unknown output in settings map even when unset", func(t *testing.T) {
		outputs, err := Open(Config{
			Selections: []string{"stdout"},
			Settings:   map[Name]Setting{"bogus": {}},
		})
		assert.Nil(t, outputs)
		assert.EqualError(t, err, `invalid output "bogus" (allowed: summary, junit, stdout, file, statsd-datadog, statsd-gcp)`)
	})

	t.Run("non-configurable output in settings map", func(t *testing.T) {
		for _, tc := range []struct {
			label      string
			selections []string
			key        Name
			setting    Setting
		}{
			{label: "selected summary", selections: []string{"summary"}, key: "summary", setting: Setting{Value: "x", Set: true}},
			{label: "selected stdout", selections: []string{"stdout"}, key: "stdout", setting: Setting{Value: "x", Set: true}},
			{label: "unselected unset summary", selections: []string{"stdout"}, key: "summary", setting: Setting{}},
		} {
			t.Run(tc.label, func(t *testing.T) {
				outputs, err := Open(Config{
					Selections: tc.selections,
					Settings:   map[Name]Setting{tc.key: tc.setting},
				})
				assert.Nil(t, outputs)
				require.Error(t, err)
				assert.EqualError(t, err, `output "`+string(tc.key)+`" takes no setting`)
				assert.NotContains(t, err.Error(), "--")
				assert.NotContains(t, err.Error(), "-o")
			})
		}
	})

	t.Run("rejects invalid settings map keys in deterministic sorted order", func(t *testing.T) {
		outputs, err := Open(Config{
			Selections: []string{"stdout"},
			Settings: map[Name]Setting{
				"zebra":   {Value: "z", Set: true},
				"summary": {Value: "s", Set: true},
				"alpha":   {Value: "a", Set: false},
			},
		})
		assert.Nil(t, outputs)
		assert.EqualError(t, err, `invalid output "alpha" (allowed: summary, junit, stdout, file, statsd-datadog, statsd-gcp)`)

		outputs, err = Open(Config{
			Selections: []string{"stdout", "summary"},
			Settings: map[Name]Setting{
				"summary": {Value: "s", Set: true},
				"stdout":  {Value: "o", Set: true},
			},
		})
		assert.Nil(t, outputs)
		assert.EqualError(t, err, `output "stdout" takes no setting`)
	})

	t.Run("setting for unselected output", func(t *testing.T) {
		outputs, err := Open(Config{
			Selections: []string{"stdout"},
			Settings:   map[Name]Setting{"file": {Value: "out.log", Set: true}},
		})
		assert.Nil(t, outputs)
		var unselected UnselectedSettingError
		require.ErrorAs(t, err, &unselected)
		assert.Equal(t, Name("file"), unselected.Output)
		assert.NotContains(t, err.Error(), "--")
		assert.NotContains(t, err.Error(), "-o")
	})

	t.Run("setting without any selection", func(t *testing.T) {
		outputs, err := Open(Config{
			Settings: map[Name]Setting{"statsd-datadog": {Value: "127.0.0.1:8125", Set: true}},
		})
		assert.Nil(t, outputs)
		var unselected UnselectedSettingError
		require.ErrorAs(t, err, &unselected)
		assert.Equal(t, Name("statsd-datadog"), unselected.Output)
	})

	t.Run("multiple unselected settings fail in catalog order", func(t *testing.T) {
		outputs, err := Open(Config{
			Selections: []string{"stdout"},
			Settings: map[Name]Setting{
				"file":  {Value: "out.log", Set: true},
				"junit": {Value: "report.xml", Set: true},
			},
		})
		assert.Nil(t, outputs)
		var unselected UnselectedSettingError
		require.ErrorAs(t, err, &unselected)
		assert.Equal(t, Name("junit"), unselected.Output)
	})

	t.Run("empty setting for selected output", func(t *testing.T) {
		outputs, err := Open(Config{
			Selections: []string{"junit"},
			Settings:   map[Name]Setting{"junit": {Value: "", Set: true}},
		})
		assert.Nil(t, outputs)
		var empty EmptySettingError
		require.ErrorAs(t, err, &empty)
		assert.Equal(t, Name("junit"), empty.Output)
		assert.NotContains(t, err.Error(), "--")
		assert.NotContains(t, err.Error(), "-o")
	})
}

func TestOpenReportsInitErrorForUnwritablePath(t *testing.T) {
	t.Parallel()
	parent := filepath.Join(t.TempDir(), "regular-file")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o644))

	outputs, err := Open(Config{
		Selections: []string{"file"},
		Settings:   map[Name]Setting{"file": {Value: filepath.Join(parent, "out.log"), Set: true}},
	})
	assert.Nil(t, outputs)
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "output errors:\n  file: init failed: "), err.Error())
	assert.Contains(t, err.Error(), "not a directory")
}

// fakeAdapter fails on demand. Each error func gets the call number, from 1.
type fakeAdapter struct {
	publishErr func(n int) error
	summaryErr error
	closeErr   error

	received []prober.Result
	closed   bool
}

func (f *fakeAdapter) Publish(res prober.Result) error {
	f.received = append(f.received, res)
	if f.publishErr != nil {
		return f.publishErr(len(f.received))
	}
	return nil
}

func (f *fakeAdapter) PublishSummary(prober.Summary) error { return f.summaryErr }

func (f *fakeAdapter) Close() error {
	f.closed = true
	return f.closeErr
}

// fakeBuilders builds a constructor map from fakes. A nil fake fails to start
// with startErr.
func fakeBuilders(fakes map[Name]*fakeAdapter, startErr error) map[Name]constructorFunc {
	builders := make(map[Name]constructorFunc, len(fakes))
	for name, fake := range fakes {
		builders[name] = func(Config) (publisher, error) {
			if fake == nil {
				return nil, startErr
			}
			return fake, nil
		}
	}
	return builders
}

func TestOpenClosesStartedOutputsWhenOneFailsToStart(t *testing.T) {
	t.Parallel()
	file := &fakeAdapter{closeErr: errors.New("flush prober.log: input/output error")}
	stdout := &fakeAdapter{}
	builders := fakeBuilders(map[Name]*fakeAdapter{outputFile: file, outputStdout: stdout, outputSummary: nil}, errors.New("bad writer"))

	outputs, err := openWith(Config{Selections: []string{"stdout", "file", "summary"}}, builders)
	assert.Nil(t, outputs)
	assert.EqualError(t, err, "output errors:\n"+
		"  file: close failed: flush prober.log: input/output error\n"+
		"  summary: init failed: bad writer")
	assert.True(t, stdout.closed)
	assert.True(t, file.closed)
}

func TestOutputsKeepPublishingWhenOneOutputFails(t *testing.T) {
	t.Parallel()
	file := &fakeAdapter{publishErr: func(int) error { return errors.New("write prober.log: no space left on device") }}
	stdout := &fakeAdapter{}
	builders := fakeBuilders(map[Name]*fakeAdapter{outputFile: file, outputStdout: stdout}, nil)

	outputs, err := openWith(Config{Selections: []string{"file", "stdout"}}, builders)
	require.NoError(t, err)
	for _, url := range []string{"https://a.example.com", "https://b.example.com", "https://c.example.com"} {
		outputs.Publish(prober.Result{URL: url})
	}
	err = outputs.Finish(prober.Summary{})

	assert.EqualError(t, err, "output errors:\n  file: publish failed 3 times: write prober.log: no space left on device")
	assert.Len(t, stdout.received, 3)
	assert.True(t, file.closed)
	assert.True(t, stdout.closed)
}

func TestOutputsReportFirstMessageOfRepeatedFailures(t *testing.T) {
	t.Parallel()
	statsd := &fakeAdapter{publishErr: func(n int) error {
		return errors.New("send " + string(rune('0'+n)) + ": connection refused")
	}}
	builders := fakeBuilders(map[Name]*fakeAdapter{outputStatsdDatadog: statsd}, nil)

	outputs, err := openWith(Config{Selections: []string{"statsd-datadog"}}, builders)
	require.NoError(t, err)
	outputs.Publish(prober.Result{})
	outputs.Publish(prober.Result{})

	assert.EqualError(t, outputs.Finish(prober.Summary{}), "output errors:\n  statsd-datadog: publish failed 2 times: send 1: connection refused")
}

func TestOutputsReportSummaryFailureAndStillClose(t *testing.T) {
	t.Parallel()
	junit := &fakeAdapter{summaryErr: errors.New("write prober.xml: permission denied")}
	summary := &fakeAdapter{}
	builders := fakeBuilders(map[Name]*fakeAdapter{outputJunit: junit, outputSummary: summary}, nil)

	outputs, err := openWith(Config{Selections: []string{"junit", "summary"}}, builders)
	require.NoError(t, err)

	assert.EqualError(t, outputs.Finish(prober.Summary{}), "output errors:\n  junit: publish summary failed: write prober.xml: permission denied")
	assert.True(t, junit.closed)
	assert.True(t, summary.closed)
}

func TestOutputsCloseEveryOutputAfterACloseFails(t *testing.T) {
	t.Parallel()
	file := &fakeAdapter{closeErr: errors.New("close prober.log: bad file descriptor")}
	gcp := &fakeAdapter{closeErr: errors.New("statsd client already closed")}
	stdout := &fakeAdapter{publishErr: func(int) error { return errors.New("broken pipe") }}
	builders := fakeBuilders(map[Name]*fakeAdapter{outputFile: file, outputStatsdGCP: gcp, outputStdout: stdout}, nil)

	outputs, err := openWith(Config{Selections: []string{"file", "statsd-gcp", "stdout"}}, builders)
	require.NoError(t, err)
	outputs.Publish(prober.Result{})

	assert.EqualError(t, outputs.Finish(prober.Summary{}), "output errors:\n"+
		"  file: close failed: close prober.log: bad file descriptor\n"+
		"  statsd-gcp: close failed: statsd client already closed\n"+
		"  stdout: publish failed: broken pipe")
	assert.True(t, file.closed)
	assert.True(t, gcp.closed)
	assert.True(t, stdout.closed)
}

type parsedStatsDMetric struct {
	value string
	tags  []string
}

func parseStatsDMetrics(t *testing.T, raw string) map[string]parsedStatsDMetric {
	t.Helper()
	metrics := make(map[string]parsedStatsDMetric)
	for line := range strings.SplitSeq(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		head, tagPart, hasTags := strings.Cut(line, "|#")
		name, valAndType, ok := strings.Cut(head, ":")
		require.True(t, ok, "malformed statsd line: %q", line)
		var tags []string
		if hasTags && tagPart != "" {
			tags = strings.Split(tagPart, ",")
		}
		metrics[name] = parsedStatsDMetric{value: valAndType, tags: tags}
	}
	return metrics
}

func listenUDP(t *testing.T) (net.PacketConn, string) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn, conn.LocalAddr().String()
}

func readDatagrams(t *testing.T, conn net.PacketConn, expected ...string) string {
	t.Helper()
	var got bytes.Buffer
	buf := make([]byte, 65535)
	deadline := time.Now().Add(2 * time.Second)
	for len(expected) > 0 && !containsAll(got.String(), expected) {
		require.NoError(t, conn.SetReadDeadline(deadline))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return got.String()
			}
			require.NoError(t, err)
		}
		got.Write(buf[:n])
		got.WriteByte('\n')
	}

	// Drain any remaining queued packets with an immediate read deadline.
	for {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Millisecond)))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return got.String()
			}
			require.NoError(t, err)
		}
		got.Write(buf[:n])
		got.WriteByte('\n')
	}
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }
