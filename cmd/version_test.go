package cmd

import (
	"bytes"
	"testing"

	"github.com/lnrdll/prober/internal/buildinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionCmd(t *testing.T) {
	oldVersion := buildinfo.Version
	oldCommit := buildinfo.Commit
	oldDate := buildinfo.Date
	defer func() {
		buildinfo.Version = oldVersion
		buildinfo.Commit = oldCommit
		buildinfo.Date = oldDate
	}()

	t.Run("prints version only when commit metadata is unset", func(t *testing.T) {
		buildinfo.Version = "dev"
		buildinfo.Commit = "none"
		buildinfo.Date = "unknown"

		buf := &bytes.Buffer{}
		versionCmd.SetOut(buf)
		versionCmd.SetErr(buf)

		versionCmd.Run(versionCmd, nil)

		assert.Equal(t, "prober dev\n", buf.String())
	})

	t.Run("prints version commit and date when build metadata is set", func(t *testing.T) {
		buildinfo.Version = "v1.2.3"
		buildinfo.Commit = "abc1234"
		buildinfo.Date = "2026-05-19 10:11:12 UTC"

		buf := &bytes.Buffer{}
		versionCmd.SetOut(buf)
		versionCmd.SetErr(buf)

		versionCmd.Run(versionCmd, nil)

		require.NotEmpty(t, buf.String())
		assert.Equal(t, "prober v1.2.3 (abc1234 2026-05-19 10:11:12 UTC)\n", buf.String())
	})
}
