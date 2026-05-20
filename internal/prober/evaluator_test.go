package prober

import (
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/lnrdll/prober/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestCompileTargetAssertions(t *testing.T) {
	tests := []struct {
		name          string
		targets       []config.Target
		expectedError string
		assertFunc    func(*testing.T, []config.Target)
	}{
		{
			name: "applies default assertion when none provided",
			targets: []config.Target{
				{Name: "test-default", URL: "http://example.com"},
			},
			assertFunc: func(t *testing.T, compiledTargets []config.Target) {
				assert.Len(t, compiledTargets, 1)
				assert.Len(t, compiledTargets[0].Assertions, 1)
				assert.Equal(t, "status >= 200 && status < 400", compiledTargets[0].Assertions[0])
				assert.Len(t, compiledTargets[0].CompiledCel, 1)
			},
		},
		{
			name: "skips disabled targets",
			targets: []config.Target{
				{Name: "test-disabled", URL: "http://example.com", Disabled: true},
				{Name: "test-enabled", URL: "http://example.com", Assertions: []string{"status == 200"}},
			},
			assertFunc: func(t *testing.T, compiledTargets []config.Target) {
				// Disabled target should not have its assertions compiled
				assert.Len(t, compiledTargets, 2)
				assert.Len(t, compiledTargets[0].CompiledCel, 0) // Disabled target
				assert.Len(t, compiledTargets[1].CompiledCel, 1) // Enabled target
			},
		},
		{
			name: "returns error for invalid CEL",
			targets: []config.Target{
				{Name: "test-invalid-cel", URL: "http://example.com", Assertions: []string{"status === 200"}}, // Invalid operator
			},
			expectedError: "Syntax error",
		},
		{
			name: "compiles multiple assertions",
			targets: []config.Target{
				{Name: "test-multiple", URL: "http://example.com", Assertions: []string{"status == 200", "body.contains('hello')"}},
			},
			assertFunc: func(t *testing.T, compiledTargets []config.Target) {
				assert.Len(t, compiledTargets, 1)
				assert.Len(t, compiledTargets[0].Assertions, 2)
				assert.Len(t, compiledTargets[0].CompiledCel, 2)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiledTargets, err := CompileTargetAssertions(tt.targets)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				assert.Nil(t, compiledTargets)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, compiledTargets)
				if tt.assertFunc != nil {
					tt.assertFunc(t, compiledTargets)
				}
			}
		})
	}
}

func TestEvaluatePrecompiled(t *testing.T) {
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
