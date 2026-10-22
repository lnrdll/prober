package cmd

import (
	"fmt"

	"github.com/lnrdll/prober/internal/prober"
	"github.com/spf13/cobra"
)

type lintOptions struct {
	configPath string
}

func newLintCmd() *cobra.Command {
	opts := &lintOptions{}
	lintCmd := &cobra.Command{
		Use:   "lint",
		Short: "Validate manifest structure and CEL assertions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}

			if _, err := prober.Prepare(cfg); err != nil {
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Configuration schema and CEL expressions are valid.")
			return err
		},
	}

	addConfigFlag(lintCmd, &opts.configPath)
	return lintCmd
}
