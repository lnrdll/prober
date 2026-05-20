package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"lnrdll/prober/internal/prober"
)

var lintConfigPath string

var lintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Validate manifest structure and CEL assertions.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(lintConfigPath)
		if err != nil {
			return err
		}

		if _, err := prober.CompileTargetAssertions(cfg.Targets); err != nil {
			return fmt.Errorf("validate assertions: %w", err)
		}

		fmt.Println("Configuration schema and CEL expressions are valid.")
		return nil
	},
}

func init() {
	addConfigFlag(lintCmd, &lintConfigPath)
	RootCmd.AddCommand(lintCmd)
}
