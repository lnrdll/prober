package output

import (
	"bytes"
	"encoding/json"
	"log/slog"
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
	ctx.Set("string", &str)
	ctx.Set("bool", &flag)

	assert.Equal(t, "value", ctx.GetString("string"))
	assert.True(t, ctx.GetBool("bool"))
	assert.Equal(t, "", ctx.GetString("missing"))
	assert.False(t, ctx.GetBool("missing"))
}

func TestFilePublisherFactory(t *testing.T) {
	hook := Registry["file"]

	t.Run("returns nil when no path configured", func(t *testing.T) {
		ctx := NewRuntimeContext()
		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.Nil(t, pub)
	})

	t.Run("opens file when configured", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.log")
		ctx := NewRuntimeContext()
		ctx.Set("log_file_path", stringPtr(path))

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
	hook := Registry["stdout"]

	t.Run("returns nil when disabled", func(t *testing.T) {
		disabled := false
		ctx := NewRuntimeContext()
		ctx.Set("enable_stdout", &disabled)

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.Nil(t, pub)
	})

	t.Run("returns publisher when enabled", func(t *testing.T) {
		enabled := true
		ctx := NewRuntimeContext()
		ctx.Set("enable_stdout", &enabled)

		pub, err := hook.Factory(ctx)
		require.NoError(t, err)
		assert.IsType(t, &StdoutPublisher{}, pub)
		assert.NoError(t, pub.Close())
	})
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

func stringPtr(v string) *string { return &v }
