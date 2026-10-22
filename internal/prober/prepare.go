package prober

import (
	"fmt"

	"cel.dev/cel-go/cel"
	"github.com/lnrdll/prober/internal/config"
)

const defaultAssertion = "status >= 200 && status < 400"

// Prepare validates cfg and compiles the assertions of every enabled target,
// falling back to the default assertion for targets that have none. It works
// on copies, so cfg is never modified and repeated calls give the same result.
// Disabled targets are validated but not compiled.
func Prepare(cfg config.Config) ([]config.Target, error) {
	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	env, err := newCELEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to build restricted CEL env: %w", err)
	}

	targets := make([]config.Target, len(cfg.Targets))
	for i, target := range cfg.Targets {
		if target.Disabled {
			target.CompiledCel = nil
			targets[i] = target
			continue
		}

		assertions := target.Assertions
		if len(assertions) == 0 {
			assertions = []string{defaultAssertion}
		}

		programs := make([]cel.Program, 0, len(assertions))
		for _, expr := range assertions {
			ast, iss := env.Compile(expr)
			if iss.Err() != nil {
				return nil, fmt.Errorf("compile assertions: target %q failed compiling assertion %q: %v", target.Name, expr, iss.Err())
			}

			prog, err := env.Program(ast)
			if err != nil {
				return nil, fmt.Errorf("compile assertions: target %q failed compiling assertion %q: %w", target.Name, expr, err)
			}
			programs = append(programs, prog)
		}

		target.Assertions = append([]string(nil), assertions...)
		target.CompiledCel = programs
		targets[i] = target
	}
	return targets, nil
}
