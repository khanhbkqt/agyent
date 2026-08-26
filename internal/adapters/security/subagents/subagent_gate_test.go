package subagents

import (
	"testing"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubagentEvaluator(t *testing.T) {
	cfg := config.SubagentGuardrailConfig{
		MaxConcurrentWorkers: 3,
		MaxCascadeDepth:      1,
		Roles: map[string]config.RolePolicyConfig{
			"researcher": {
				AllowedTools:    []string{"view_file", "grep_search", "find_by_name", "search_web"},
				DisallowedTools: []string{"run_command", "write_to_file", "invoke_subagent", "define_subagent"},
			},
			"coder": {
				AllowedTools:    []string{"*"},
				DisallowedTools: []string{"define_subagent"},
			},
		},
	}

	evaluator := NewEvaluator(cfg)

	// Test 1: Researcher attempting run_command -> Denied
	decision, err := evaluator.EvaluateSubagent(true, 1, "run_command", "researcher", 1)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "explicitly disallowed")

	// Test 2: Researcher reading a file -> Allowed
	decision, err = evaluator.EvaluateSubagent(true, 1, "view_file", "researcher", 1)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, decision.Decision)

	// Test 3: Subagent attempting to spawn subagent when depth = 1 -> Denied
	decision, err = evaluator.EvaluateSubagent(true, 1, "invoke_subagent", "coder", 1)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "max cascade depth")

	// Test 4: Concurrency limit reached -> Denied
	decision, err = evaluator.EvaluateSubagent(false, 0, "invoke_subagent", "", 3)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "Concurrency limit exceeded")

	// Test 5: Root agent invoking subagent within quota -> Allowed
	decision, err = evaluator.EvaluateSubagent(false, 0, "invoke_subagent", "", 1)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, decision.Decision)
}
