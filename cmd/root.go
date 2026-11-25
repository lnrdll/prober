package cmd

import (
	"github.com/lnrdll/prober/internal/prober"
	"github.com/spf13/cobra"
)

// NewRootCmd builds the prober command tree. Every call returns new commands
// with their own flag values, so invocations share no configuration.
func NewRootCmd() *cobra.Command {
	return newRootCmd(prober.Runner{})
}

func newRootCmd(runner prober.Runner) *cobra.Command {
	root := &cobra.Command{
		Use:           "prober",
		Short:         "Probe HTTP(S) targets from a YAML manifest.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(newRunCmd(runner), newLintCmd(), newVersionCmd())
	return root
}
