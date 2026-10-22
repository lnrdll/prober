package prober

import (
	"testing"

	"cel.dev/cel-go/cel"
	"github.com/stretchr/testify/assert"
)

func TestEvaluatePrecompiled(t *testing.T) {
	env, err := newCELEnv()
	assert.NoError(t, err)

	compileExpr := func(expr string) cel.Program {
		ast, iss := env.Compile(expr)
		assert.NoError(t, iss.Err())
		prog, err := env.Program(ast)
		assert.NoError(t, err)
		return prog
	}

	tests := []struct {
		name          string
		expression    string
		context       EvalContext
		expected      bool
		expectedError string
	}{
		{
			name:       "true result for passing expression",
			expression: "status == 200",
			context:    EvalContext{Status: 200},
			expected:   true,
		},
		{
			name:       "false result for failing expression",
			expression: "status == 200",
			context:    EvalContext{Status: 400},
			expected:   false,
		},
		{
			name:       "expression with body content",
			expression: "body.contains('hello')",
			context:    EvalContext{Body: "this is a hello world"},
			expected:   true,
		},
		{
			name:       "expression with headers",
			expression: "headers['Content-Type'] == 'application/json'",
			context:    EvalContext{Headers: map[string]string{"Content-Type": "application/json"}},
			expected:   true,
		},
		{
			name:       "expression with latency",
			expression: "latency_ms < 100",
			context:    EvalContext{LatencyMS: 50},
			expected:   true,
		},
		{
			name:       "expression with SSL days left",
			expression: "ssl_days_left > 30",
			context:    EvalContext{SSLDaysLeft: 60},
			expected:   true,
		},
		{
			name:          "error when expression is non-boolean",
			expression:    "status + 1",
			context:       EvalContext{Status: 200},
			expectedError: "assertion output did not yield boolean",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog := compileExpr(tt.expression)
			result, err := EvaluatePrecompiled(prog, tt.context)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				assert.False(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
