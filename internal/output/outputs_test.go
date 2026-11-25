package output

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lnrdll/prober/internal/prober"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	var buf bytes.Buffer
	outputs, err := Open(Config{Selections: []string{"stdout", " stdout "}, Writer: &buf})
	require.NoError(t, err)

	outputs.Publish(prober.Result{URL: "http://example.com", Up: true, Status: 200})
	require.NoError(t, outputs.Finish(prober.Summary{}))

	assert.Len(t, bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")), 1)
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

func TestOutputsAppendFileLog(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.log")
	outputs, err := Open(Config{Selections: []string{"file"}, File: Setting{Value: path, Set: true}})
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
}

func TestOutputsWriteJUnitReport(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "report.xml")
	outputs, err := Open(Config{Selections: []string{"junit"}, JUnit: Setting{Value: path, Set: true}})
	require.NoError(t, err)

	failing := prober.Result{Name: "api", URL: "https://api.example.com", Up: false, Status: 500, LatencyMS: 35, FailedAssertion: "status == 200"}
	outputs.Publish(failing)
	require.NoError(t, outputs.Finish(prober.Summary{Total: 1, Failed: 1, Duration: 1500 * time.Millisecond, Results: []prober.Result{failing}}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var report junitTestSuite
	require.NoError(t, xml.Unmarshal(data, &report))
	assert.Equal(t, 1, report.Tests)
	assert.Equal(t, 1, report.Failures)
	assert.Equal(t, "1.500", report.Time)
	require.Len(t, report.TestCases, 1)
	require.NotNil(t, report.TestCases[0].Failure)
	assert.Equal(t, "status == 200", report.TestCases[0].Failure.Message)
}

func TestOutputsSendStatsDGauges(t *testing.T) {
	t.Parallel()
	res := prober.Result{URL: "https://example.com", Up: true, Status: 200, LatencyMS: 42, Tags: map[string]string{"environment": "prod", "team": "web"}}

	t.Run("datadog", func(t *testing.T) {
		conn, addr := listenUDP(t)
		outputs, err := Open(Config{Selections: []string{"statsd-datadog"}, Datadog: Setting{Value: addr, Set: true}})
		require.NoError(t, err)

		outputs.Publish(res)
		require.NoError(t, outputs.Finish(prober.Summary{}))

		got := readDatagrams(t, conn)
		assert.Contains(t, got, "custom_monitoring.uptime_prober.status:1|g")
		assert.Contains(t, got, "custom_monitoring.uptime_prober.latency_ms:42|g")
	})

	t.Run("gcp", func(t *testing.T) {
		conn, addr := listenUDP(t)
		outputs, err := Open(Config{Selections: []string{"statsd-gcp"}, GCP: Setting{Value: addr, Set: true}})
		require.NoError(t, err)

		outputs.Publish(res)
		require.NoError(t, outputs.Finish(prober.Summary{}))

		got := readDatagrams(t, conn)
		assert.Contains(t, got, "uptime_prober.status.env_prod.team_web:1|g")
		assert.Contains(t, got, "uptime_prober.latency_ms.env_prod.team_web:42|g")
	})
}

func TestOpenAppliesDefaults(t *testing.T) {
	// Serial: t.Chdir mutates process working directory.
	t.Chdir(t.TempDir())

	outputs, err := Open(Config{Selections: []string{"file", "junit"}})
	require.NoError(t, err)
	outputs.Publish(prober.Result{URL: "http://example.com", Up: true})
	require.NoError(t, outputs.Finish(prober.Summary{}))

	assert.FileExists(t, DefaultFilePath)
	assert.FileExists(t, DefaultJUnitPath)
}

func TestOpenAppliesStatsDDefaults(t *testing.T) {
	t.Parallel()
	var got []string
	builders := map[Name]constructorFunc{
		OutputStatsdDatadog: func(cfg Config) (publisher, error) { got = append(got, cfg.Datadog.Value); return &fakeAdapter{}, nil },
		OutputStatsdGCP:     func(cfg Config) (publisher, error) { got = append(got, cfg.GCP.Value); return &fakeAdapter{}, nil },
	}

	outputs, err := openWith(Config{Selections: []string{"statsd-datadog", "statsd-gcp"}}, builders)
	require.NoError(t, err)
	require.NoError(t, outputs.Finish(prober.Summary{}))

	assert.Equal(t, []string{"127.0.0.1:8125", "127.0.0.1:8125"}, got)
}

func TestOpenRejectsBadSelections(t *testing.T) {
	t.Parallel()

	t.Run("unknown output", func(t *testing.T) {
		outputs, err := Open(Config{Selections: []string{"unknown"}})
		assert.Nil(t, outputs)
		assert.EqualError(t, err, `invalid output "unknown" (allowed: summary, junit, stdout, file, statsd-datadog, statsd-gcp)`)
	})

	t.Run("setting for unselected output", func(t *testing.T) {
		outputs, err := Open(Config{Selections: []string{"stdout"}, File: Setting{Value: "out.log", Set: true}})
		assert.Nil(t, outputs)
		var unselected UnselectedSettingError
		require.ErrorAs(t, err, &unselected)
		assert.Equal(t, OutputFile, unselected.Output)
		assert.NotContains(t, err.Error(), "--")
		assert.NotContains(t, err.Error(), "-o")
	})

	t.Run("setting without any selection", func(t *testing.T) {
		outputs, err := Open(Config{Datadog: Setting{Value: DefaultStatsDAddr, Set: true}})
		assert.Nil(t, outputs)
		var unselected UnselectedSettingError
		require.ErrorAs(t, err, &unselected)
		assert.Equal(t, OutputStatsdDatadog, unselected.Output)
	})

	t.Run("empty setting for selected output", func(t *testing.T) {
		outputs, err := Open(Config{Selections: []string{"junit"}, JUnit: Setting{Value: "", Set: true}})
		assert.Nil(t, outputs)
		var empty EmptySettingError
		require.ErrorAs(t, err, &empty)
		assert.Equal(t, OutputJunit, empty.Output)
		assert.NotContains(t, err.Error(), "--")
		assert.NotContains(t, err.Error(), "-o")
	})
}

func TestOpenReportsInitErrorForUnwritablePath(t *testing.T) {
	t.Parallel()
	parent := filepath.Join(t.TempDir(), "regular-file")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o644))

	outputs, err := Open(Config{Selections: []string{"file"}, File: Setting{Value: filepath.Join(parent, "out.log"), Set: true}})
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
	builders := fakeBuilders(map[Name]*fakeAdapter{OutputFile: file, OutputStdout: stdout, OutputSummary: nil}, errors.New("bad writer"))

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
	builders := fakeBuilders(map[Name]*fakeAdapter{OutputFile: file, OutputStdout: stdout}, nil)

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
	builders := fakeBuilders(map[Name]*fakeAdapter{OutputStatsdDatadog: statsd}, nil)

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
	builders := fakeBuilders(map[Name]*fakeAdapter{OutputJunit: junit, OutputSummary: summary}, nil)

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
	builders := fakeBuilders(map[Name]*fakeAdapter{OutputFile: file, OutputStatsdGCP: gcp, OutputStdout: stdout}, nil)

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
