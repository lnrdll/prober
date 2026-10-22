package prober

import (
	"fmt"

	"cel.dev/cel-go/cel"
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

// newCELEnv declares the variables an assertion can read.
func newCELEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("status", cel.IntType),
		cel.Variable("body", cel.StringType),
		cel.Variable("latency_ms", cel.IntType),
		cel.Variable("ssl_days_left", cel.IntType),
		cel.Variable("ssl_issuer", cel.StringType),
		cel.Variable("ssl_subject", cel.StringType),
		cel.Variable("ssl_dns_names", cel.StringType),
		cel.Variable("headers", cel.MapType(cel.StringType, cel.StringType)),
	)
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
