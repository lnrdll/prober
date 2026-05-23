package cmd

import (
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
	globalRunCtx        = output.NewRuntimeContext()
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run probes from a target manifest.",
	RunE: func(cmd *cobra.Command, args []string) error {
		globalRunCtx.Set(output.StdoutWriterKey, cmd.OutOrStdout())

		cfg, err := loadConfig(runConfigPath)
		if err != nil {
			return err
		}
		if err := config.Validate(cfg); err != nil {
			return fmt.Errorf("validate config: %w", err)
		}

		selectedOutputs, err := output.ParseSelections(runOutputs)
		if err != nil {
			return err
		}

		for _, name := range output.Names() {
			hook, ok := output.Get(name)
			if !ok || hook.SetupFlags == nil {
				continue
			}

			configuredValue := globalRunCtx.GetString(string(name))
			if globalRunCtx.OutputSelected(name) && configuredValue == "" {
				return fmt.Errorf("--%s is required when -o %s is set", name, name)
			}
			if !globalRunCtx.OutputSelected(name) && configuredValue != "" {
				return fmt.Errorf("--%s requires -o %s", name, name)
			}
		}

		compiledTargets, err := prober.CompileTargetAssertions(cfg.Targets)
		if err != nil {
			return fmt.Errorf("compile probe assertions: %w", err)
		}

		var activePublishers []output.Publisher
		for _, name := range selectedOutputs {
			hook, ok := output.Get(name)
			if !ok {
				return fmt.Errorf("initialize output %q: not registered", name)
			}
			pub, err := hook.Factory(globalRunCtx)
			if err != nil {
				return fmt.Errorf("initialize output %q: %w", name, err)
			}
			if pub != nil {
				activePublishers = append(activePublishers, pub)
			}
		}

		if len(selectedOutputs) == 0 {
			stdoutHook, ok := output.Get(output.OutputStdout)
			if !ok {
				return fmt.Errorf("initialize default output %q: not registered", output.OutputStdout)
			}
			stdoutFallback, err := stdoutHook.Factory(globalRunCtx)
			if err != nil {
				return fmt.Errorf("initialize default output %q: %w", output.OutputStdout, err)
			}
			if stdoutFallback != nil {
				activePublishers = append(activePublishers, stdoutFallback)
			}
		}

		summary := prober.Execute(compiledTargets, activePublishers)

		for _, pub := range activePublishers {
			if err := pub.Close(); err != nil {
				return fmt.Errorf("close output publisher: %w", err)
			}
		}

		if failOnTargetFailure && summary.Failed > 0 {
			return fmt.Errorf("%d target(s) failed", summary.Failed)
		}

		return nil
	},
}

func init() {
	addConfigFlag(runCmd, &runConfigPath)
	runCmd.Flags().BoolVar(&failOnTargetFailure, "fail-on-target-failure", false, "Exit non-zero when any target fails")
	runCmd.Flags().StringArrayVarP(&runOutputs, "output", "o", nil, "Enable an output backend (repeatable): "+output.JoinNames(output.Names()))
	globalRunCtx.Set(string(output.SelectionKey), &runOutputs)

	output.LinkFlagBinders(
		func(name, value, usage string, target *string) { runCmd.Flags().StringVar(target, name, value, usage) },
		func(name string, value bool, usage string, target *bool) {
			runCmd.Flags().BoolVar(target, name, value, usage)
		},
	)

	for _, hook := range output.Registry {
		if hook.SetupFlags != nil {
			hook.SetupFlags(globalRunCtx, func(bindFunc func()) { bindFunc() })
		}
	}
}
