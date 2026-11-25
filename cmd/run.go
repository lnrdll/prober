package cmd

import (
	"errors"
	"fmt"

	"github.com/lnrdll/prober/internal/output"
	"github.com/lnrdll/prober/internal/prober"

	"github.com/spf13/cobra"
)

type runOptions struct {
	configPath          string
	failOnTargetFailure bool
	outputs             []string
	filePath            string
	junitPath           string
	datadogAddr         string
	gcpAddr             string
}

func newRunCmd(runner prober.Runner) *cobra.Command {
	opts := &runOptions{}
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run probes from a target manifest.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			plan, err := prober.Prepare(cfg)
			if err != nil {
				return err
			}

			setting := func(name output.Name, value string) output.Setting {
				return output.Setting{Value: value, Set: cmd.Flags().Changed(string(name))}
			}
			outputs, err := output.Open(output.Config{
				Selections: opts.outputs,
				Writer:     cmd.OutOrStdout(),
				File:       setting(output.OutputFile, opts.filePath),
				JUnit:      setting(output.OutputJunit, opts.junitPath),
				Datadog:    setting(output.OutputStatsdDatadog, opts.datadogAddr),
				GCP:        setting(output.OutputStatsdGCP, opts.gcpAddr),
			})
			if err != nil {
				return formatOutputOpenError(err)
			}

			summary := runner.Execute(cmd.Context(), plan, outputs.Publish)
			outputErr := outputs.Finish(summary)

			var targetErr error
			if opts.failOnTargetFailure && summary.Failed > 0 {
				targetErr = fmt.Errorf("%d target(s) failed", summary.Failed)
			}

			return errors.Join(outputErr, targetErr)
		},
	}

	addConfigFlag(runCmd, &opts.configPath)
	runCmd.Flags().BoolVar(&opts.failOnTargetFailure, "fail-on-target-failure", false, "Exit non-zero when any target fails")
	runCmd.Flags().StringArrayVarP(&opts.outputs, "output", "o", nil, "Enable an output backend (repeatable): "+output.JoinNames(output.Names()))
	runCmd.Flags().StringVar(&opts.filePath, string(output.OutputFile), output.DefaultFilePath, "Append structured JSON log lines to a file")
	runCmd.Flags().StringVar(&opts.junitPath, string(output.OutputJunit), output.DefaultJUnitPath, "Write a JUnit XML report to a file")
	runCmd.Flags().StringVar(&opts.datadogAddr, string(output.OutputStatsdDatadog), output.DefaultStatsDAddr, "UDP network address location pointing to local Datadog daemon")
	runCmd.Flags().StringVar(&opts.gcpAddr, string(output.OutputStatsdGCP), output.DefaultStatsDAddr, "UDP network address location pointing to local GCP Ops Agent")
	return runCmd
}

func formatOutputOpenError(err error) error {
	var unselected output.UnselectedSettingError
	if errors.As(err, &unselected) {
		return fmt.Errorf("--%s requires -o %s", unselected.Output, unselected.Output)
	}
	var empty output.EmptySettingError
	if errors.As(err, &empty) {
		return fmt.Errorf("--%s is required when -o %s is set", empty.Output, empty.Output)
	}
	return err
}
