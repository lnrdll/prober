package cmd

import (
	"errors"
	"fmt"

	"github.com/lnrdll/prober/internal/config"
	"github.com/lnrdll/prober/internal/output"
	"github.com/lnrdll/prober/internal/prober"

	"github.com/spf13/cobra"
)

var (
	runConfigPath       string
	failOnTargetFailure bool
	runOutputs          []string
	runFilePath         string
	runJUnitPath        string
	runDatadogAddr      string
	runGCPAddr          string
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run probes from a target manifest.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(runConfigPath)
		if err != nil {
			return err
		}
		if err := config.Validate(cfg); err != nil {
			return fmt.Errorf("validate config: %w", err)
		}

		compiledTargets, err := prober.CompileTargetAssertions(cfg.Targets)
		if err != nil {
			return fmt.Errorf("compile probe assertions: %w", err)
		}

		setting := func(name output.Name, value string) output.Setting {
			return output.Setting{Value: value, Set: cmd.Flags().Changed(string(name))}
		}
		outputs, err := output.Open(output.Config{
			Selections: runOutputs,
			Writer:     cmd.OutOrStdout(),
			File:       setting(output.OutputFile, runFilePath),
			JUnit:      setting(output.OutputJunit, runJUnitPath),
			Datadog:    setting(output.OutputStatsdDatadog, runDatadogAddr),
			GCP:        setting(output.OutputStatsdGCP, runGCPAddr),
		})
		if err != nil {
			return err
		}

		summary := prober.Execute(compiledTargets, outputs.Publish)
		outputErr := outputs.Finish(summary)

		var targetErr error
		if failOnTargetFailure && summary.Failed > 0 {
			targetErr = fmt.Errorf("%d target(s) failed", summary.Failed)
		}

		return errors.Join(outputErr, targetErr)
	},
}

func init() {
	addConfigFlag(runCmd, &runConfigPath)
	runCmd.Flags().BoolVar(&failOnTargetFailure, "fail-on-target-failure", false, "Exit non-zero when any target fails")
	runCmd.Flags().StringArrayVarP(&runOutputs, "output", "o", nil, "Enable an output backend (repeatable): "+output.JoinNames(output.Names()))
	runCmd.Flags().StringVar(&runFilePath, string(output.OutputFile), output.DefaultFilePath, "Append structured JSON log lines to a file")
	runCmd.Flags().StringVar(&runJUnitPath, string(output.OutputJunit), output.DefaultJUnitPath, "Write a JUnit XML report to a file")
	runCmd.Flags().StringVar(&runDatadogAddr, string(output.OutputStatsdDatadog), output.DefaultStatsDAddr, "UDP network address location pointing to local Datadog daemon")
	runCmd.Flags().StringVar(&runGCPAddr, string(output.OutputStatsdGCP), output.DefaultStatsDAddr, "UDP network address location pointing to local GCP Ops Agent")
}
