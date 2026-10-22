package prober

import (
	"fmt"

	"cel.dev/cel-go/cel"
	"github.com/lnrdll/prober/internal/config"
)

const defaultAssertion = "status >= 200 && status < 400"

// Plan is the prepared form of a manifest's targets. Only Prepare builds one,
// and Execute accepts nothing else.
type Plan struct {
	targets []preparedTarget
}

// preparedTarget is a target with its compiled assertions. Disabled targets
// have none.
type preparedTarget struct {
	config.Target
	assertions []assertion
}

// Prepare validates cfg and compiles the assertions of every enabled target,
// falling back to the default assertion for targets that have none. It works
// on copies, so cfg is never modified and repeated calls give the same result.
// Disabled targets are validated but not compiled.
func Prepare(cfg config.Config) (Plan, error) {
	if err := config.Validate(cfg); err != nil {
		return Plan{}, fmt.Errorf("validate config: %w", err)
	}

	env, err := newCELEnv()
	if err != nil {
		return Plan{}, fmt.Errorf("failed to build restricted CEL env: %w", err)
	}

	targets := make([]preparedTarget, len(cfg.Targets))
	for i, target := range cfg.Targets {
		if target.Disabled {
			targets[i] = preparedTarget{Target: target}
			continue
		}

		exprs := target.Assertions
		if len(exprs) == 0 {
			exprs = []string{defaultAssertion}
		}

		assertions := make([]assertion, 0, len(exprs))
		for _, expr := range exprs {
			ast, iss := env.Compile(expr)
			if iss.Err() != nil {
				return Plan{}, fmt.Errorf("compile assertions: target %q failed compiling assertion %q: %v", target.Name, expr, iss.Err())
			}
			// A dynamic output passes here; worker checks its value at run time.
			if out := ast.OutputType(); !out.IsExactType(cel.BoolType) && !out.IsExactType(cel.DynType) {
				return Plan{}, fmt.Errorf("compile assertions: target %q assertion %q must return a boolean", target.Name, expr)
			}

			prog, err := env.Program(ast)
			if err != nil {
				return Plan{}, fmt.Errorf("compile assertions: target %q failed compiling assertion %q: %w", target.Name, expr, err)
			}
			assertions = append(assertions, assertion{text: expr, program: prog})
		}

		targets[i] = preparedTarget{Target: target, assertions: assertions}
	}
	return Plan{targets: targets}, nil
}
