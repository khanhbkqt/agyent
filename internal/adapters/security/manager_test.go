package security

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"agyent/internal/adapters/security/ipc"
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

func TestSecurityManager_PerTurnIsolation(t *testing.T) {
	cfg := config.GetEffectiveSecurityPreset("balanced")
	mgr := NewManager(cfg, nil, nil)
	ctx := context.Background()

	// Register 3 distinct active turns with opposing security presets
	mgr.RegisterActiveTurn(domain.TurnSecurityContext{
		ConversationID: "conv-unrestricted",
		SessionKey:     "session-admin",
		WorkspaceDir:   "/ws/admin",
		Preset:         domain.PresetUnrestricted,
		AgentName:      "admin_bot",
	})
	mgr.RegisterActiveTurn(domain.TurnSecurityContext{
		ConversationID: "conv-strict",
		SessionKey:     "session-strict",
		WorkspaceDir:   "/ws/strict",
		Preset:         domain.PresetStrict,
		AgentName:      "strict_bot",
	})
	mgr.RegisterActiveTurn(domain.TurnSecurityContext{
		ConversationID: "conv-readonly",
		SessionKey:     "session-readonly",
		WorkspaceDir:   "/ws/readonly",
		Preset:         domain.PresetReadOnly,
		AgentName:      "readonly_bot",
	})

	// 1. Unrestricted agent should allow destructive / unwhitelisted commands
	dec1, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:       "run_command",
		Args:           map[string]interface{}{"CommandLine": "python custom_script.py"},
		ConversationID: "conv-unrestricted",
		WorkspaceDir:   "/ws/admin",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec1.Decision, "Unrestricted turn must allow custom command")

	// 2. Strict agent evaluating the EXACT same command at the same time must be blocked
	dec2, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:       "run_command",
		Args:           map[string]interface{}{"CommandLine": "python custom_script.py"},
		ConversationID: "conv-strict",
		WorkspaceDir:   "/ws/strict",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec2.Decision, "Strict turn must deny unwhitelisted command")
	assert.Contains(t, dec2.Reason, "Strict")

	// 3. ReadOnly agent attempting file write must be blocked
	dec3, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:       "write_to_file",
		Args:           map[string]interface{}{"TargetFile": "/ws/readonly/test.txt"},
		ConversationID: "conv-readonly",
		WorkspaceDir:   "/ws/readonly",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec3.Decision, "ReadOnly turn must deny write_to_file")
	assert.Contains(t, dec3.Reason, "Read Only")

	// 4. Unregistering turn cleans up context cleanly
	mgr.UnregisterActiveTurn("conv-strict", "/ws/strict")
	_, found := mgr.ResolveTurnContext("conv-strict", "/ws/strict")
	assert.False(t, found)
}

func TestSecurityManager_HeavyConcurrentStressTest(t *testing.T) {
	cfg := config.GetEffectiveSecurityPreset("balanced")
	mgr := NewManager(cfg, nil, nil)
	ctx := context.Background()

	// Register 5 different agent profiles
	presets := []struct {
		agentName string
		convID    string
		wsDir     string
		preset    domain.SecurityPreset
	}{
		{"agent_unrestricted", "conv-unres", "/ws/unres", domain.PresetUnrestricted},
		{"agent_developer", "conv-dev", "/ws/dev", domain.PresetDeveloper},
		{"agent_balanced", "conv-bal", "/ws/bal", domain.PresetBalanced},
		{"agent_strict", "conv-strict", "/ws/strict", domain.PresetStrict},
		{"agent_readonly", "conv-ro", "/ws/ro", domain.PresetReadOnly},
	}

	for _, p := range presets {
		mgr.RegisterActiveTurn(domain.TurnSecurityContext{
			ConversationID: p.convID,
			SessionKey:     "session-" + p.agentName,
			WorkspaceDir:   p.wsDir,
			Preset:         p.preset,
			AgentName:      p.agentName,
		})
	}

	numGoroutines := 50
	iterations := 20
	errChan := make(chan error, numGoroutines*iterations)

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		agentIdx := i % len(presets)
		target := presets[agentIdx]

		go func(p struct {
			agentName string
			convID    string
			wsDir     string
			preset    domain.SecurityPreset
		}, gId int) {
			defer wg.Done()

			for iter := 0; iter < iterations; iter++ {
				// Concurrent whitelist / grant mutations happening simultaneously
				if iter%5 == 0 {
					mgr.GrantSessionPermission("session-"+p.agentName, fmt.Sprintf("npm run task-%d", gId))
				}
				if iter%10 == 0 {
					mgr.AddWhitelistEntry(fmt.Sprintf("go test ./pkg/%d", gId))
				}

				// Test 1: Destructive command rm -rf /
				dec, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
					ToolName:       "run_command",
					Args:           map[string]interface{}{"CommandLine": "rm -rf /"},
					ConversationID: p.convID,
					WorkspaceDir:   p.wsDir,
				})
				if err != nil {
					errChan <- fmt.Errorf("unexpected error: %w", err)
					return
				}
				if p.preset == domain.PresetUnrestricted {
					if dec.Decision != domain.DecisionAllow {
						errChan <- fmt.Errorf("unrestricted agent blocked on rm -rf /: %s", dec.Reason)
						return
					}
				} else {
					if dec.Decision != domain.DecisionDeny {
						errChan <- fmt.Errorf("agent %s with preset %s allowed rm -rf /", p.agentName, p.preset)
						return
					}
				}

				// Test 2: File write
				decWrite, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
					ToolName:       "write_to_file",
					Args:           map[string]interface{}{"TargetFile": filepath.Join(p.wsDir, "test.txt")},
					ConversationID: p.convID,
					WorkspaceDir:   p.wsDir,
				})
				if err != nil {
					errChan <- fmt.Errorf("unexpected error on write: %w", err)
					return
				}
				if p.preset == domain.PresetReadOnly {
					if decWrite.Decision != domain.DecisionDeny {
						errChan <- fmt.Errorf("readonly agent allowed file write: %v", decWrite)
						return
					}
				}

				// Test 3: Unknown custom script execution
				decCustom, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
					ToolName:       "run_command",
					Args:           map[string]interface{}{"CommandLine": "python secret_worker.py"},
					ConversationID: p.convID,
					WorkspaceDir:   p.wsDir,
				})
				if err != nil {
					errChan <- fmt.Errorf("unexpected error on custom cmd: %w", err)
					return
				}
				if p.preset == domain.PresetStrict {
					if decCustom.Decision != domain.DecisionDeny {
						errChan <- fmt.Errorf("strict agent allowed unwhitelisted python cmd: %v", decCustom)
						return
					}
				}
			}
		}(target, i)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		require.NoError(t, err)
	}
}

func TestSecurityManager_RealIPCServerClientE2E(t *testing.T) {
	cfg := config.GetEffectiveSecurityPreset("balanced")
	mgr := NewManager(cfg, nil, nil)

	addr := "127.0.0.1:49993"
	server := ipc.NewServer(mgr, addr, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, server.Start(ctx))
	defer server.Stop()

	time.Sleep(30 * time.Millisecond)

	// Register 2 live turns for distinct agents
	mgr.RegisterActiveTurn(domain.TurnSecurityContext{
		ConversationID: "conv-ipc-unres",
		SessionKey:     "session-admin",
		WorkspaceDir:   "/tmp/ws_admin",
		Preset:         domain.PresetUnrestricted,
		AgentName:      "admin_bot",
	})
	mgr.RegisterActiveTurn(domain.TurnSecurityContext{
		ConversationID: "conv-ipc-strict",
		SessionKey:     "session-auditor",
		WorkspaceDir:   "/tmp/ws_audit",
		Preset:         domain.PresetStrict,
		AgentName:      "audit_bot",
	})

	client := ipc.NewClient(addr)

	// 1. Real TCP IPC Request for Unrestricted Agent
	resp1, err := client.SendHookRequest(domain.HookRequest{
		HookType:       "pre",
		ConversationID: "conv-ipc-unres",
		WorkspacePaths: []string{"/tmp/ws_admin"},
		ToolCall: domain.HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "python custom_deploy.py"},
		},
		StepIdx: 1,
	}, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "allow", resp1.Decision, "Real IPC call for unrestricted agent must return allow")

	// 2. Real TCP IPC Request for Strict Agent (Unwhitelisted command)
	resp2, err := client.SendHookRequest(domain.HookRequest{
		HookType:       "pre",
		ConversationID: "conv-ipc-strict",
		WorkspacePaths: []string{"/tmp/ws_audit"},
		ToolCall: domain.HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "python custom_deploy.py"},
		},
		StepIdx: 2,
	}, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "deny", resp2.Decision, "Real IPC call for strict agent must return deny")
	assert.Contains(t, resp2.Reason, "Strict")

	// 3. Real TCP IPC Request for Strict Agent (Whitelisted command)
	resp3, err := client.SendHookRequest(domain.HookRequest{
		HookType:       "pre",
		ConversationID: "conv-ipc-strict",
		WorkspacePaths: []string{"/tmp/ws_audit"},
		ToolCall: domain.HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "go test ./..."},
		},
		StepIdx: 3,
	}, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "allow", resp3.Decision, "Real IPC call for strict agent with whitelisted cmd must return allow")
}
