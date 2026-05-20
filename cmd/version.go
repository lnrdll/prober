package cmd

import (
	"fmt"

	"github.com/lnrdll/prober/internal/buildinfo"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the prober version",
	Run: func(cmd *cobra.Command, args []string) {
		if buildinfo.Commit != "none" && buildinfo.Date != "unknown" {
			cmd.Println(fmt.Sprintf("prober %s (%s %s)", buildinfo.Version, buildinfo.Commit, buildinfo.Date))
			return
		}

		cmd.Println(fmt.Sprintf("prober %s", buildinfo.Version))
	},
}

func init() {
	RootCmd.AddCommand(versionCmd)
}
