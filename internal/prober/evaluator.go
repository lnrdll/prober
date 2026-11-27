package prober

import (
	"fmt"

	"cel.dev/cel-go/cel"
)

// assertion pairs an assertion's text with its compiled program, so a failure
// always reports the expression that ran.
type assertion struct {
	text    string
	program cel.Program
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

// evaluate runs the assertion's program against a probe result.
func (a assertion) evaluate(res Result) (bool, error) {
	input := map[string]any{
		"status":        int64(res.Status),
		"body":          res.Body,
		"latency_ms":    res.LatencyMS,
		"ssl_days_left": int64(res.SSLDaysLeft),
		"ssl_issuer":    res.SSLIssuer,
		"ssl_subject":   res.SSLSubject,
		"ssl_dns_names": res.SSLDNSNames,
		"headers":       res.Headers,
	}

	out, _, err := a.program.Eval(input)
	if err != nil {
		return false, err
	}

	boolVal, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("assertion output did not yield boolean")
	}
	return boolVal, nil
}
