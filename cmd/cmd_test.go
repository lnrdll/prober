package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lnrdll/prober/internal/output"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// executeRoot runs a fresh root command with args and returns what it wrote to
// stdout and the error it returned.
func executeRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(io.Discard)
	err := root.Execute()
	return out.String(), err
}

// newTargetServer starts a local server that answers every request with 200.
func newTargetServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server
}

// manifestTarget is one target in a test manifest. An empty assertion leaves
// the target on the default assertion.
type manifestTarget struct {
	name      string
	assertion string
}

// writeManifest writes a manifest whose targets all probe url, and returns its
// path.
func writeManifest(t *testing.T, url string, targets ...manifestTarget) string {
	t.Helper()
	manifest := "targets:\n"
	for _, target := range targets {
		manifest += "  - name: " + target.name + "\n    url: " + url + "\n"
		if target.assertion != "" {
			manifest += "    assertions:\n      - " + target.assertion + "\n"
		}
	}
	path := filepath.Join(t.TempDir(), "targets.yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0644))
	return path
}

func TestRunCmd(t *testing.T) {
	server := newTargetServer(t)
	failing := manifestTarget{name: "failing", assertion: "status == 500"}
	passing := manifestTarget{name: "passing", assertion: "status == 200"}

	t.Run("prints summary and succeeds when failures are allowed", func(t *testing.T) {
		path := writeManifest(t, server.URL, failing)
		filePath := filepath.Join(t.TempDir(), "prober.log")

		out, err := executeRoot(t, "run", "-c", path, "-o", "summary", "-o", "file", "--file", filePath)
		require.NoError(t, err)
		assert.Contains(t, out, "Summary: total=1")
		assert.Contains(t, out, "FAILED "+server.URL)
	})

	t.Run("summary output suppresses default stdout output", func(t *testing.T) {
		path := writeManifest(t, server.URL, failing)

		out, err := executeRoot(t, "run", "-c", path, "-o", "summary")
		require.NoError(t, err)
		assert.Contains(t, out, "Summary: total=1")
		assert.Contains(t, out, "FAILED "+server.URL)
		assert.NotContains(t, out, "{")
	})

	t.Run("writes stdout json by default", func(t *testing.T) {
		path := writeManifest(t, server.URL, passing)

		out, err := executeRoot(t, "run", "-c", path)
		require.NoError(t, err)
		assert.Contains(t, out, `"msg":"Uptime check passed"`)
		assert.NotContains(t, out, "Summary:")
	})

	t.Run("returns error when failures are enforced", func(t *testing.T) {
		path := writeManifest(t, server.URL, failing)
		filePath := filepath.Join(t.TempDir(), "prober.log")

		out, err := executeRoot(t, "run", "-c", path, "--fail-on-target-failure", "-o", "summary", "-o", "file", "--file", filePath)
		assert.ErrorContains(t, err, "1 target(s) failed")
		assert.Contains(t, out, "Summary: total=1")
	})

	t.Run("does not print summary when summary output is not selected", func(t *testing.T) {
		path := writeManifest(t, server.URL, passing)
		filePath := filepath.Join(t.TempDir(), "prober.log")

		out, err := executeRoot(t, "run", "-c", path, "--fail-on-target-failure", "-o", "file", "--file", filePath)
		require.NoError(t, err)
		assert.Empty(t, out)
	})

	t.Run("writes junit report when selected", func(t *testing.T) {
		path := writeManifest(t, server.URL, manifestTarget{"homepage", "status == 200"}, manifestTarget{"api", "status == 500"})
		reportPath := filepath.Join(t.TempDir(), "report.xml")

		out, err := executeRoot(t, "run", "-c", path, "-o", "junit", "--junit", reportPath)
		require.NoError(t, err)
		assert.Empty(t, out)

		data, readErr := os.ReadFile(reportPath)
		require.NoError(t, readErr)
		assert.Contains(t, string(data), "<testsuite")
		assert.Contains(t, string(data), "name=\"homepage\"")
		assert.Contains(t, string(data), "name=\"api\"")
		assert.Contains(t, string(data), "status == 500")
	})

	t.Run("errors on unknown output", func(t *testing.T) {
		path := writeManifest(t, server.URL, manifestTarget{name: "passing"})

		_, err := executeRoot(t, "run", "-c", path, "-o", "unknown")
		assert.ErrorContains(t, err, "invalid output")
		assert.ErrorContains(t, err, string(output.OutputStdout))
	})

	t.Run("uses default file config when output is selected", func(t *testing.T) {
		path := writeManifest(t, server.URL, manifestTarget{name: "passing"})
		t.Chdir(t.TempDir())

		_, err := executeRoot(t, "run", "-c", path, "-o", "file")
		require.NoError(t, err)
		_, statErr := os.Stat(output.DefaultFilePath)
		require.NoError(t, statErr)
	})

	t.Run("uses default junit config when output is selected", func(t *testing.T) {
		path := writeManifest(t, server.URL, manifestTarget{name: "passing"})
		t.Chdir(t.TempDir())

		_, err := executeRoot(t, "run", "-c", path, "-o", "junit")
		require.NoError(t, err)
		_, statErr := os.Stat(output.DefaultJUnitPath)
		require.NoError(t, statErr)
	})

	t.Run("requires output selection when an output setting is provided", func(t *testing.T) {
		path := writeManifest(t, server.URL, manifestTarget{name: "passing"})

		_, err := executeRoot(t, "run", "-c", path, "--file", filepath.Join(t.TempDir(), "prober.log"))
		assert.ErrorContains(t, err, "--file requires -o file")
	})

	t.Run("flag defaults are exposed in help metadata", func(t *testing.T) {
		runCmd, _, err := NewRootCmd().Find([]string{"run"})
		require.NoError(t, err)
		assert.Equal(t, output.DefaultFilePath, runCmd.Flags().Lookup(string(output.OutputFile)).DefValue)
		assert.Equal(t, output.DefaultJUnitPath, runCmd.Flags().Lookup(string(output.OutputJunit)).DefValue)
		assert.Equal(t, output.DefaultStatsDAddr, runCmd.Flags().Lookup(string(output.OutputStatsdDatadog)).DefValue)
		assert.Equal(t, output.DefaultStatsDAddr, runCmd.Flags().Lookup(string(output.OutputStatsdGCP)).DefValue)
		assert.Equal(t, "false", runCmd.Flags().Lookup("fail-on-target-failure").DefValue)
	})

	t.Run("repeated invocations share no configuration", func(t *testing.T) {
		path := writeManifest(t, server.URL, failing)
		reportPath := filepath.Join(t.TempDir(), "report.xml")
		filePath := filepath.Join(t.TempDir(), "prober.log")

		out, err := executeRoot(t, "run", "-c", path, "--fail-on-target-failure", "-o", "summary", "-o", "junit", "--junit", reportPath)
		assert.ErrorContains(t, err, "1 target(s) failed")
		assert.Contains(t, out, "Summary: total=1")
		require.NoError(t, os.Remove(reportPath))

		out, err = executeRoot(t, "run", "-c", path, "-o", "file", "--file", filePath)
		require.NoError(t, err)
		assert.Empty(t, out)
		assert.NoFileExists(t, reportPath)
		assert.FileExists(t, filePath)
	})
}

func TestRunCmdReturnsOutputFailureWhenTargetFailuresAreAllowed(t *testing.T) {
	out, err := runWithUnwritableJUnit(t, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "output errors:\n  junit: publish summary failed: ")
	assert.NotContains(t, err.Error(), "target(s) failed")
	assert.Contains(t, out, "Summary: total=1 passed=0 failed=1")
}

func TestRunCmdReportsOutputFailureBeforeTargetFailures(t *testing.T) {
	_, err := runWithUnwritableJUnit(t, true)
	require.Error(t, err)
	assert.Regexp(t, `^output errors:\n  junit: publish summary failed: .+\n1 target\(s\) failed$`, err.Error())
}

// runWithUnwritableJUnit runs one failing target against a local server with
// the JUnit report pointed under a regular file, and returns the run's stdout
// and error.
func runWithUnwritableJUnit(t *testing.T, failOnTargets bool) (string, error) {
	t.Helper()
	server := newTargetServer(t)
	path := writeManifest(t, server.URL, manifestTarget{name: "failing", assertion: "status == 500"})
	regularFile := filepath.Join(t.TempDir(), "regular-file")
	require.NoError(t, os.WriteFile(regularFile, []byte("x"), 0644))

	args := []string{"run", "-c", path, "-o", "summary", "-o", "junit", "--junit", filepath.Join(regularFile, "report.xml")}
	if failOnTargets {
		args = append(args, "--fail-on-target-failure")
	}
	return executeRoot(t, args...)
}

func TestLoadConfig(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: test\n    url: http://example.com\n    timeout: 15\n"), 0644))

		cfg, err := loadConfig(path)
		require.NoError(t, err)
		require.Len(t, cfg.Targets, 1)
		assert.Equal(t, "test", cfg.Targets[0].Name)
		assert.Equal(t, "http://example.com", cfg.Targets[0].URL)
		assert.Equal(t, 15, cfg.Targets[0].Timeout)
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := loadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
		assert.ErrorContains(t, err, "read config")
	})

	t.Run("invalid yaml", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "invalid.yaml")
		require.NoError(t, os.WriteFile(path, []byte("targets: ["), 0644))

		_, err := loadConfig(path)
		assert.ErrorContains(t, err, "parse config")
	})
}

func TestAddConfigFlag(t *testing.T) {
	cmd := &cobra.Command{RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	var configPath string
	addConfigFlag(cmd, &configPath)

	flag := cmd.Flags().Lookup("config")
	require.NotNil(t, flag)
	assert.Equal(t, "c", flag.Shorthand)

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.ErrorContains(t, err, "required flag(s) \"config\" not set")

	cmd.SetArgs([]string{"-c", "targets.yaml"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "targets.yaml", configPath)
}

func TestLintCmd(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "valid.yaml")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: test\n    url: http://example.com\n    assertions:\n      - status == 200\n"), 0644))

		out, err := executeRoot(t, "lint", "-c", path)
		require.NoError(t, err)
		assert.Equal(t, "Configuration schema and CEL expressions are valid.\n", out)
	})

	t.Run("invalid config path", func(t *testing.T) {
		_, err := executeRoot(t, "lint", "-c", filepath.Join(t.TempDir(), "missing.yaml"))
		assert.ErrorContains(t, err, "read config")
	})
}

// TestRunAndLintRejectSameManifests checks that run and lint reject the same
// bad manifest with the same error text.
func TestRunAndLintRejectSameManifests(t *testing.T) {
	server := newTargetServer(t)

	tests := []struct {
		name     string
		manifest string
		expected string
	}{
		{
			name:     "structurally invalid manifest",
			manifest: "targets:\n  - name: test\n    url: " + server.URL + "\n    method: get\n",
			expected: `^validate config: target "test": method must be uppercase$`,
		},
		{
			name:     "invalid CEL",
			manifest: "targets:\n  - name: test\n    url: " + server.URL + "\n    assertions:\n      - status === 200\n",
			expected: `^compile assertions: target "test" failed compiling assertion "status === 200": .*Syntax error`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "targets.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.manifest), 0644))

			runOut, runErr := executeRoot(t, "run", "-c", path, "-o", "summary")
			lintOut, lintErr := executeRoot(t, "lint", "-c", path)

			require.Error(t, runErr)
			require.Error(t, lintErr)
			assert.Regexp(t, tt.expected, runErr.Error())
			assert.Equal(t, runErr.Error(), lintErr.Error())
			assert.Empty(t, runOut)
			assert.Empty(t, lintOut)
		})
	}
}
