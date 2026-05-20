package cmd

import (
	"github.com/spf13/cobra"
)

var RootCmd = &cobra.Command{
	Use:           "prober",
	Short:         "Probe HTTP(S) targets from a YAML manifest.",
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	RootCmd.AddCommand(runCmd)
}
