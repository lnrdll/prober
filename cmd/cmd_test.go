package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/lnrdll/prober/internal/output"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCmdRunE(t *testing.T) {
	oldPath := runConfigPath
	oldFail := failOnTargetFailure
	oldOutputs := append([]string(nil), runOutputs...)
	oldOut := runCmd.OutOrStdout()
	fileOutput := ""
	datadogOutput := ""
	gcpOutput := ""
	globalRunCtx.Set(string(output.OutputFile), &fileOutput)
	globalRunCtx.Set(string(output.OutputStatsdDatadog), &datadogOutput)
	globalRunCtx.Set(string(output.OutputStatsdGCP), &gcpOutput)
	defer func() {
		runConfigPath = oldPath
		failOnTargetFailure = oldFail
		runOutputs = oldOutputs
		runCmd.SetOut(oldOut)
	}()

	t.Run("prints summary and succeeds when failures are allowed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "targets.yaml")
		filePath := filepath.Join(t.TempDir(), "prober.log")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: failing\n    url: https://example.com\n    assertions:\n      - status == 500\n"), 0644))
		runConfigPath = path
		failOnTargetFailure = false
		runOutputs = []string{string(output.OutputSummary), string(output.OutputFile)}
		fileOutput = filePath
		datadogOutput = ""
		gcpOutput = ""

		buf := &bytes.Buffer{}
		runCmd.SetOut(buf)

		err := runCmd.RunE(runCmd, nil)
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "Summary: total=1")
		assert.Contains(t, buf.String(), "FAILED https://example.com")
	})

	t.Run("summary output suppresses default stdout output", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "targets.yaml")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: failing\n    url: https://example.com\n    assertions:\n      - status == 500\n"), 0644))
		runConfigPath = path
		failOnTargetFailure = false
		runOutputs = []string{string(output.OutputSummary)}
		fileOutput = ""
		datadogOutput = ""
		gcpOutput = ""

		buf := &bytes.Buffer{}
		runCmd.SetOut(buf)

		stdout := os.Stdout
		r, w, err := os.Pipe()
		require.NoError(t, err)
		os.Stdout = w

		err = runCmd.RunE(runCmd, nil)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		os.Stdout = stdout

		stdoutData, readErr := io.ReadAll(r)
		require.NoError(t, readErr)
		assert.Empty(t, string(stdoutData))
		assert.Contains(t, buf.String(), "Summary: total=1")
		assert.Contains(t, buf.String(), "FAILED https://example.com")
	})

	t.Run("returns error when failures are enforced", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "targets.yaml")
		filePath := filepath.Join(t.TempDir(), "prober.log")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: failing\n    url: https://example.com\n    assertions:\n      - status == 500\n"), 0644))
		runConfigPath = path
		failOnTargetFailure = true
		runOutputs = []string{string(output.OutputSummary), string(output.OutputFile)}
		fileOutput = filePath
		datadogOutput = ""
		gcpOutput = ""

		buf := &bytes.Buffer{}
		runCmd.SetOut(buf)

		err := runCmd.RunE(runCmd, nil)
		assert.ErrorContains(t, err, "1 target(s) failed")
		assert.Contains(t, buf.String(), "Summary: total=1")
	})

	t.Run("validates config before compilation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "invalid.yaml")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: test\n    url: https://example.com\n    method: get\n"), 0644))
		runConfigPath = path
		failOnTargetFailure = true
		runOutputs = []string{string(output.OutputFile)}
		fileOutput = filepath.Join(t.TempDir(), "prober.log")
		datadogOutput = ""
		gcpOutput = ""

		err := runCmd.RunE(runCmd, nil)
		assert.ErrorContains(t, err, "validate config")
		assert.ErrorContains(t, err, "method must be uppercase")
	})

	t.Run("does not print summary when summary output is not selected", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "targets.yaml")
		filePath := filepath.Join(t.TempDir(), "prober.log")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: passing\n    url: https://example.com\n    assertions:\n      - status == 200\n"), 0644))
		runConfigPath = path
		failOnTargetFailure = true
		runOutputs = []string{string(output.OutputFile)}
		fileOutput = filePath
		datadogOutput = ""
		gcpOutput = ""

		buf := &bytes.Buffer{}
		runCmd.SetOut(buf)

		err := runCmd.RunE(runCmd, nil)
		require.NoError(t, err)
		assert.Empty(t, buf.String())
	})

	t.Run("prints summary when selected as an output", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "targets.yaml")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: failing\n    url: https://example.com\n    assertions:\n      - status == 500\n"), 0644))
		runConfigPath = path
		failOnTargetFailure = false
		runOutputs = []string{string(output.OutputSummary)}
		fileOutput = ""
		datadogOutput = ""
		gcpOutput = ""

		buf := &bytes.Buffer{}
		runCmd.SetOut(buf)

		err := runCmd.RunE(runCmd, nil)
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "Summary: total=1")
		assert.Contains(t, buf.String(), "FAILED https://example.com")
	})

	t.Run("errors on unknown output", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "targets.yaml")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: passing\n    url: https://example.com\n"), 0644))
		runConfigPath = path
		failOnTargetFailure = false
		runOutputs = []string{"unknown"}
		fileOutput = ""
		datadogOutput = ""
		gcpOutput = ""

		err := runCmd.RunE(runCmd, nil)
		assert.ErrorContains(t, err, "invalid output")
		assert.ErrorContains(t, err, string(output.OutputStdout))
	})

	t.Run("requires config when output is selected", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "targets.yaml")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: passing\n    url: https://example.com\n"), 0644))
		runConfigPath = path
		failOnTargetFailure = false
		runOutputs = []string{string(output.OutputFile)}
		fileOutput = ""
		datadogOutput = ""
		gcpOutput = ""

		err := runCmd.RunE(runCmd, nil)
		assert.ErrorContains(t, err, "--file is required when -o file is set")
	})

	t.Run("requires output selection when config is provided", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "targets.yaml")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: passing\n    url: https://example.com\n"), 0644))
		runConfigPath = path
		failOnTargetFailure = false
		runOutputs = nil
		fileOutput = filepath.Join(t.TempDir(), "prober.log")
		datadogOutput = ""
		gcpOutput = ""

		err := runCmd.RunE(runCmd, nil)
		assert.ErrorContains(t, err, "--file requires -o file")
	})
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

func TestLintCmdRunE(t *testing.T) {
	oldPath := lintConfigPath
	defer func() { lintConfigPath = oldPath }()

	t.Run("valid config", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "valid.yaml")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: test\n    url: http://example.com\n    assertions:\n      - status == 200\n"), 0644))
		lintConfigPath = path

		stdout := os.Stdout
		r, w, err := os.Pipe()
		require.NoError(t, err)
		os.Stdout = w

		err = lintCmd.RunE(lintCmd, nil)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		os.Stdout = stdout

		out, err := bytes.NewBuffer(nil).ReadFrom(r)
		_ = out
		assert.NoError(t, err)
	})

	t.Run("invalid config path", func(t *testing.T) {
		lintConfigPath = filepath.Join(t.TempDir(), "missing.yaml")
		err := lintCmd.RunE(lintCmd, nil)
		assert.ErrorContains(t, err, "read config")
	})

	t.Run("invalid assertions", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "invalid-cel.yaml")
		require.NoError(t, os.WriteFile(path, []byte("targets:\n  - name: test\n    url: http://example.com\n    assertions:\n      - status === 200\n"), 0644))
		lintConfigPath = path

		err := lintCmd.RunE(lintCmd, nil)
		assert.ErrorContains(t, err, "validate assertions")
	})
}
