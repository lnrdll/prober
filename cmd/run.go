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
	settings            map[output.Name]*string
}

func newRunCmd(runner prober.Runner) *cobra.Command {
	descriptors := output.SettingDescriptors()
	opts := &runOptions{
		settings: make(map[output.Name]*string, len(descriptors)),
	}
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run probes from a target manifest.",
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := prober.Prepare(opts.configPath)
			if err != nil {
				return err
			}

			settings := make(map[output.Name]output.Setting, len(descriptors))
			for _, d := range descriptors {
				settings[d.Name] = output.Setting{
					Value: *opts.settings[d.Name],
					Set:   cmd.Flags().Changed(string(d.Name)),
				}
			}
			outputs, err := output.Open(output.Config{
				Selections: opts.outputs,
				Writer:     cmd.OutOrStdout(),
				Settings:   settings,
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
	for _, d := range descriptors {
		var value string
		opts.settings[d.Name] = &value
		runCmd.Flags().StringVar(&value, string(d.Name), d.Default, d.Description)
	}
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
