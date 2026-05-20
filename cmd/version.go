package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the prober version",
	Run: func(cmd *cobra.Command, args []string) {
		if Commit != "none" && Date != "unknown" {
			cmd.Println(fmt.Sprintf("prober %s (%s %s)", Version, Commit, Date))
			return
		}

		cmd.Println(fmt.Sprintf("prober %s", Version))
	},
}

func init() {
	RootCmd.AddCommand(versionCmd)
}
