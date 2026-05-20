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
	showSummary         bool
	globalRunCtx        = output.NewRuntimeContext()
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

		var activePublishers []output.Publisher
		for name, hook := range output.Registry {
			pub, err := hook.Factory(globalRunCtx)
			if err != nil {
				return fmt.Errorf("initialize output %q: %w", name, err)
			}
			if pub != nil {
				activePublishers = append(activePublishers, pub)
			}
		}

		if len(activePublishers) == 0 {
			stdoutFallback, err := output.Registry["stdout"].Factory(globalRunCtx)
			if err != nil {
				return fmt.Errorf("initialize default output %q: %w", "stdout", err)
			}
			if stdoutFallback != nil {
				activePublishers = append(activePublishers, stdoutFallback)
			}
		}

		summary := prober.Execute(compiledTargets, activePublishers)
		if showSummary {
			printSummary(cmd, summary)
		}

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
	runCmd.Flags().BoolVar(&showSummary, "summary", false, "Print a run summary after execution")

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

func printSummary(cmd *cobra.Command, summary prober.Summary) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Summary: total=%d passed=%d failed=%d skipped=%d duration=%s\n", summary.Total, summary.Passed, summary.Failed, summary.Skipped, summary.Duration.Round(1e6))
	for _, res := range summary.Results {
		if res.Up {
			continue
		}
		reason := res.Error
		if reason == "" {
			reason = res.FailedAssertion
		}
		if reason == "" {
			reason = "unknown failure"
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "FAILED %s status=%d reason=%s\n", res.URL, res.Status, reason)
	}
}
