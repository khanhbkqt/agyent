package agy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreflightCanaryCheckAcceptsVerifiedHookDenial(t *testing.T) {
	run := func(_ context.Context, binary string, args []string, workspace string, env []string, stdin []byte) ([]byte, []byte, error) {
		assert.Equal(t, "agy", binary)
		assert.Contains(t, args, "agy-proj-worker")
		assert.Contains(t, args, "--sandbox")
		assert.Equal(t, "/tmp/worker", workspace)
		assert.Contains(t, string(stdin), preflightCanaryCommand)
		assert.True(t, containsEnv(env, "AGYENT_TURN_ID="+preflightTurnID))
		return []byte(`{"event":"step_update","step_update":{"state":"ERROR","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","error":{"type":"TOOL_ERROR","message":"tool call denied by pre-tool hook: Invalid or expired TurnID"}}}}`), nil, nil
	}

	err := preflightCanaryCheck(context.Background(), "agy", "agy-proj-worker", "/tmp/worker", run)
	require.NoError(t, err)
}

func TestPreflightCanaryCheckRejectsNativePermissionDenial(t *testing.T) {
	run := func(context.Context, string, []string, string, []string, []byte) ([]byte, []byte, error) {
		return []byte(`{"event":"result","result":{"status":"ERROR","denied_actions":[{"tool":"run_command"}]}}`),
			[]byte(`a tool required the "command" permission that headless mode cannot prompt for`), errors.New("exit status 1")
	}

	err := preflightCanaryCheck(context.Background(), "agy", "agy-proj-worker", "/tmp/worker", run)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "native project grant was rejected")
}

func TestPreflightCanaryCheckRejectsExecutedCanary(t *testing.T) {
	run := func(context.Context, string, []string, string, []string, []byte) ([]byte, []byte, error) {
		return []byte(`{"event":"step_update","step_update":{"state":"DONE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","output":"AGYENT_PREFLIGHT_CANARY"}}}`), nil, nil
	}

	err := preflightCanaryCheck(context.Background(), "agy", "agy-proj-worker", "/tmp/worker", run)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "canary command executed")
}

func containsEnv(env []string, target string) bool {
	for _, item := range env {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}
