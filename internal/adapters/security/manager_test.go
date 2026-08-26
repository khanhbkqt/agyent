package security

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockHITLApprovalPort struct {
	requestApprovalFn func(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error)
}

func (m *mockHITLApprovalPort) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
	if m.requestApprovalFn != nil {
		return m.requestApprovalFn(ctx, req)
	}
	return domain.ApprovalDecision{Approved: true, Action: "allow_once"}, nil
}

func (m *mockHITLApprovalPort) HandleCallback(ctx context.Context, callbackID string, userID int64, action string) error {
	return nil
}

func (m *mockHITLApprovalPort) CancelPendingRequest(requestID string) {}

func TestSecurityManager_EvaluateToolCall_CommandPolicies(t *testing.T) {
	cfg := config.GetEffectiveSecurityPreset("balanced")
	mockHITL := &mockHITLApprovalPort{}
	mgr := NewManager(cfg, mockHITL, nil)
	ctx := context.Background()

	// 1. Destructive command blocked
	dec, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "rm -rf /"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "forbidden pattern")

	// 2. Whitelisted command allowed
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "go test ./..."},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)

	// 3. Sensitive command with HITL approval
	mockHITL.requestApprovalFn = func(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
		return domain.ApprovalDecision{Approved: true, Action: "allow_once"}, nil
	}
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "curl https://example.com"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)
	assert.Contains(t, dec.Reason, "Approved by administrator")

	// 4. Sensitive command with HITL denial
	mockHITL.requestApprovalFn = func(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
		return domain.ApprovalDecision{Approved: false, Action: "deny"}, nil
	}
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "chmod 777 /var/data"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "rejected by user or approval timed out")

	// 5. Python inline execution command triggers HITL
	hitlTriggered := false
	mockHITL.requestApprovalFn = func(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
		hitlTriggered = true
		assert.Equal(t, "python -c \"print('hello')\"", req.CommandLine)
		return domain.ApprovalDecision{Approved: true, Action: "allow_once"}, nil
	}
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "python -c \"print('hello')\""},
	})
	require.NoError(t, err)
	assert.True(t, hitlTriggered, "python -c must trigger HITL in balanced preset")
	assert.Equal(t, domain.DecisionAllow, dec.Decision)
}

func TestSecurityManager_PresetSwitching(t *testing.T) {
	cfg := config.GetEffectiveSecurityPreset("balanced")
	mgr := NewManager(cfg, nil, nil)
	ctx := context.Background()

	// Switch to Read Only
	mgr.SetPreset(domain.PresetReadOnly)
	dec, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "ls -la"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Read Only")

	// Switch to Strict
	mgr.SetPreset(domain.PresetStrict)
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "python script.py"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Strict")

	// Switch to Unrestricted (Full Access)
	mgr.SetPreset(domain.PresetUnrestricted)
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "python -c \"print('autonomous')\""},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)
	assert.Contains(t, dec.Reason, "Unrestricted")

	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "write_to_file",
		Args:     map[string]interface{}{"TargetFile": "C:\\Windows\\Temp\\test.txt"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)
}

func TestSecurityManager_SanitizeToolOutput(t *testing.T) {
	cfg := config.GetEffectiveSecurityPreset("balanced")
	mgr := NewManager(cfg, nil, nil)
	ctx := context.Background()

	raw := "Found key: sk-proj-1234567890abcdef1234567890 and DB_PASSWORD=supersecretpass"
	sanitized, err := mgr.SanitizeToolOutput(ctx, "view_file", raw)
	require.NoError(t, err)
	assert.Contains(t, sanitized, "[REDACTED_SECRET]")
	assert.NotContains(t, sanitized, "supersecretpass")
}

func TestSecurityManager_PathJailAndDashboard(t *testing.T) {
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0755))

	cfg := config.GetEffectiveSecurityPreset("balanced")
	cfg.Filesystem.AllowedPaths = []string{workspaceDir}
	mgr := NewManager(cfg, nil, nil)
	ctx := context.Background()

	// Denied path outside
	dec, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:     "view_file",
		Args:         map[string]interface{}{"TargetFile": filepath.Join(tempDir, "other.txt")},
		WorkspaceDir: workspaceDir,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)

	// Dashboard summary check
	summary := mgr.GetDashboardSummary("session-1")
	assert.Equal(t, domain.PresetBalanced, summary.Preset)
	assert.True(t, summary.TotalEvaluations > 0)
	assert.True(t, summary.BlockedToday > 0)
}
