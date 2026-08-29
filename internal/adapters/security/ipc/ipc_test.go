package ipc

import (
	"context"
	"testing"
	"time"

	"agyent/internal/core/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSecurityManager struct {
	evaluateToolCallFn   func(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error)
	sanitizeToolOutputFn func(ctx context.Context, toolName string, output string) (string, error)
}

func (m *mockSecurityManager) EvaluateToolCall(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error) {
	if m.evaluateToolCallFn != nil {
		return m.evaluateToolCallFn(ctx, req)
	}
	return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
}

func (m *mockSecurityManager) EvaluateCommand(ctx context.Context, sessionKey string, role string, cmd string) (domain.SecurityDecision, error) {
	return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
}

func (m *mockSecurityManager) EvaluatePath(ctx context.Context, sessionKey string, workspaceDir string, targetPath string, isWrite bool) (domain.SecurityDecision, error) {
	return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
}

func (m *mockSecurityManager) EvaluateURL(ctx context.Context, urlStr string) (domain.SecurityDecision, error) {
	return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
}

func (m *mockSecurityManager) SanitizeToolOutput(ctx context.Context, toolName string, output string) (string, error) {
	if m.sanitizeToolOutputFn != nil {
		return m.sanitizeToolOutputFn(ctx, toolName, output)
	}
	return output, nil
}

func (m *mockSecurityManager) GrantSessionPermission(sessionKey string, pattern string) {}
func (m *mockSecurityManager) SetPreset(preset domain.SecurityPreset)                   {}
func (m *mockSecurityManager) SetRedactionMode(mode domain.RedactionMode)               {}
func (m *mockSecurityManager) AddWhitelistEntry(entry string)                           {}
func (m *mockSecurityManager) GetDashboardSummary(sessionKey string) domain.SecurityDashboard {
	return domain.SecurityDashboard{}
}
func (m *mockSecurityManager) EnsureWorkspaceHooks(workspaceDir string) error {
	return nil
}
func (m *mockSecurityManager) RegisterActiveTurn(turn domain.TurnSecurityContext)          {}
func (m *mockSecurityManager) UnregisterActiveTurn(convID string, workspaceDir string)     {}
func (m *mockSecurityManager) ResolveSessionKey(convID string, workspaceDir string) string { return "" }
func (m *mockSecurityManager) ResolveTurnContext(convID string, workspaceDir string) (domain.TurnSecurityContext, bool) {
	return domain.TurnSecurityContext{}, false
}

func TestIPCServerAndClient_PreToolUse(t *testing.T) {
	addr := "127.0.0.1:49988"
	mockMgr := &mockSecurityManager{
		evaluateToolCallFn: func(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error) {
			if req.ToolName == "run_command" {
				if cmd, ok := req.Args["CommandLine"].(string); ok && cmd == "rm -rf /" {
					return domain.SecurityDecision{
						Decision: domain.DecisionDeny,
						Reason:   "Destructive command blocked",
					}, nil
				}
			}
			return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
		},
	}

	server := NewServer(mockMgr, addr, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop()

	// Wait for server to start
	time.Sleep(20 * time.Millisecond)

	client := NewClient(addr)

	// Test 1: Denied command
	respDeny, err := client.SendHookRequest(domain.HookRequest{
		HookType: "pre",
		ToolCall: domain.HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "rm -rf /"},
		},
		ConversationID: "conv-123",
		StepIdx:        1,
	}, 2*time.Second)

	require.NoError(t, err)
	assert.Equal(t, "deny", respDeny.Decision)
	assert.Contains(t, respDeny.Reason, "Destructive command blocked")

	// Test 2: Allowed command
	respAllow, err := client.SendHookRequest(domain.HookRequest{
		HookType: "pre",
		ToolCall: domain.HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "go test ./..."},
		},
		ConversationID: "conv-123",
		StepIdx:        2,
	}, 2*time.Second)

	require.NoError(t, err)
	assert.Equal(t, "allow", respAllow.Decision)
}

func TestIPCClient_OfflineDefaultDeny(t *testing.T) {
	client := NewClient("127.0.0.1:49999") // Offline port

	resp, err := client.SendHookRequest(domain.HookRequest{
		HookType: "pre",
		ToolCall: domain.HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "rm -rf /"},
		},
	}, 500*time.Millisecond)

	assert.Error(t, err)
	assert.Equal(t, "deny", resp.Decision)
	assert.Contains(t, resp.Reason, "Fail-safe Default-Deny")
}
