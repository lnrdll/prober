package prober

import (
	"fmt"

	"cel.dev/cel-go/cel"
	"github.com/lnrdll/prober/internal/config"
)

type EvalContext struct {
	Status      int
	Body        string
	LatencyMS   int64
	SSLDaysLeft int
	SSLIssuer   string
	SSLSubject  string
	SSLDNSNames string
	Headers     map[string]string
}

// CompileTargetAssertions builds the CEL environment and parses expressions once at startup.
func CompileTargetAssertions(targets []config.Target) ([]config.Target, error) {
	env, err := cel.NewEnv(
		cel.Variable("status", cel.IntType),
		cel.Variable("body", cel.StringType),
		cel.Variable("latency_ms", cel.IntType),
		cel.Variable("ssl_days_left", cel.IntType),
		cel.Variable("ssl_issuer", cel.StringType),
		cel.Variable("ssl_subject", cel.StringType),
		cel.Variable("ssl_dns_names", cel.StringType),
		cel.Variable("headers", cel.MapType(cel.StringType, cel.StringType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build restricted CEL env: %w", err)
	}

	for i := range targets {
		if targets[i].Disabled {
			continue
		}

		// Default to traditional response safety boundaries if no assertions exist
		if len(targets[i].Assertions) == 0 {
			targets[i].Assertions = []string{"status >= 200 && status < 400"}
		}

		for _, expr := range targets[i].Assertions {
			ast, iss := env.Compile(expr)
			if iss.Err() != nil {
				return nil, fmt.Errorf("target '%s' failed compiling assertion '%s': %v", targets[i].Name, expr, iss.Err())
			}

			prog, err := env.Program(ast)
			if err != nil {
				return nil, fmt.Errorf("failed program instantiation: %w", err)
			}
			targets[i].CompiledCel = append(targets[i].CompiledCel, prog)
		}
	}
	return targets, nil
}

// EvaluatePrecompiled runs the specific program against data fields in memory.
func EvaluatePrecompiled(prog cel.Program, ctx EvalContext) (bool, error) {
	input := map[string]any{
		"status":        int64(ctx.Status),
		"body":          ctx.Body,
		"latency_ms":    ctx.LatencyMS,
		"ssl_days_left": int64(ctx.SSLDaysLeft),
		"ssl_issuer":    ctx.SSLIssuer,
		"ssl_subject":   ctx.SSLSubject,
		"ssl_dns_names": ctx.SSLDNSNames,
		"headers":       ctx.Headers,
	}

	out, _, err := prog.Eval(input)
	if err != nil {
		return false, err
	}

	boolVal, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("assertion output did not yield boolean")
	}
	return boolVal, nil
}
