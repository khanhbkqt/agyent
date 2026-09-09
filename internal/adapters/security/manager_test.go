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
	"agyent/internal/core/ports"
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

func (m *mockHITLApprovalPort) CancelPendingRequestsForSession(sessionKey string) {}

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

	// 6. HITL Force Kill emits EventForceKillRequested
	var capturedEvent domain.Event
	mockBus := &mockEventBusForSecMgr{
		emitFn: func(e domain.Event) {
			capturedEvent = e
		},
	}
	mgr.SetEventBus(mockBus)
	mockHITL.requestApprovalFn = func(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
		return domain.ApprovalDecision{Approved: false, Action: "force_kill"}, nil
	}
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		SessionKey:     "telegram:test:forcekill",
		ConversationID: "conv-123",
		ToolName:       "run_command",
		Args:           map[string]interface{}{"CommandLine": "chmod 777 /var/data"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Equal(t, domain.EventForceKillRequested, capturedEvent.Type)
	payload, ok := capturedEvent.Payload.(domain.ForceKillPayload)
	require.True(t, ok)
	assert.Equal(t, "telegram:test:forcekill", payload.SessionKey)
	assert.Equal(t, "conv-123", payload.ConversationID)
}

func TestSecurityManager_AskWithoutHITLPortFailsClosed(t *testing.T) {
	cfg := config.GetEffectiveSecurityPreset("balanced")
	mgr := NewManager(cfg, nil, nil)

	decision, err := mgr.EvaluateToolCall(context.Background(), domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "curl https://example.com"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "approval channel")
}

func TestSecurityManager_AntiSelfEscalation(t *testing.T) {
	cfg := config.GetEffectiveSecurityPreset("balanced")
	mgr := NewManager(cfg, nil, nil)
	ctx := context.Background()

	// 1. agyent agent preset command should be denied
	dec, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "agyent agent preset content_weaver unrestricted"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Privilege Escalation Blocked")

	// 2. agyent security preset command should be denied
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "./bin/agyent security preset unrestricted"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Privilege Escalation Blocked")

	// 3. sqlite3 touching agyent.db should be denied
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "sqlite3 ~/.agyent/agyent.db \"UPDATE agents SET security_preset='unrestricted'\""},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Privilege Escalation Blocked")

	// 4. pkill agyent should be denied
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "run_command",
		Args:     map[string]interface{}{"CommandLine": "pkill -9 agyent"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Privilege Escalation Blocked")

	// 5. write_to_file with python script connecting to agyent.db should be blocked at creation time
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "write_to_file",
		Args: map[string]interface{}{
			"TargetFile":  "hack.py",
			"CodeContent": "import sqlite3\nconn = sqlite3.connect('/home/user/.agyent/agyent.db')\nconn.execute('UPDATE agents SET security_preset=\"unrestricted\"')",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Staged Privilege Escalation Blocked")

	// 6. write_to_file with script calling agyent agent preset should be blocked at creation time
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "write_to_file",
		Args: map[string]interface{}{
			"TargetFile":  "escalate.sh",
			"CodeContent": "#!/bin/bash\nagyent agent preset content_weaver unrestricted\n",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Staged Privilege Escalation Blocked")
}

type mockEventBusForSecMgr struct {
	ports.EventBusPort
	emitFn func(domain.Event)
}

func (m *mockEventBusForSecMgr) SyncEmit(ctx context.Context, evt domain.Event) error {
	if m.emitFn != nil {
		m.emitFn(evt)
	}
	return nil
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

	// Denied directory tools outside workspace
	for _, tool := range []string{"list_dir", "grep_search", "find_by_name"} {
		argKey := "DirectoryPath"
		if tool == "grep_search" {
			argKey = "SearchPath"
		} else if tool == "find_by_name" {
			argKey = "SearchDirectory"
		}
		decTool, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
			ToolName:     tool,
			Args:         map[string]interface{}{argKey: filepath.Join(tempDir, "outside_folder")},
			WorkspaceDir: workspaceDir,
		})
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, decTool.Decision, "Tool %s should be blocked outside workspace", tool)
	}

	// Web search without Url should be allowed
	decSearch, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName: "web_search",
		Args:     map[string]interface{}{"query": "golang 1.27"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, decSearch.Decision)
	// Dashboard summary check
	summary := mgr.GetDashboardSummary("session-1")
	assert.Equal(t, domain.PresetBalanced, summary.Preset)
	assert.True(t, summary.TotalEvaluations > 0)
	assert.True(t, summary.BlockedToday > 0)
}

func TestSecurityManager_LayerSeparation_PresetCommand_ScopePaths(t *testing.T) {
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "workspace")
	extraDir := filepath.Join(tempDir, "extra_scope")
	forbiddenDir := filepath.Join(tempDir, "forbidden_outside")

	require.NoError(t, os.MkdirAll(workspaceDir, 0755))
	require.NoError(t, os.MkdirAll(extraDir, 0755))
	require.NoError(t, os.MkdirAll(forbiddenDir, 0755))

	cfg := config.GetEffectiveSecurityPreset("balanced")
	mgr := NewManager(cfg, nil, nil)
	ctx := context.Background()

	// 1. Agent has developer preset, but NO AllowedPaths.
	// Layer 1: developer allows commands like `ls -la`.
	// Layer 2: Scope is ONLY workspaceDir. Access outside workspaceDir is strictly Denied even on developer preset!
	devTurn := domain.TurnSecurityContext{
		TurnID:         "turn-dev-1",
		ConversationID: "conv-dev-1",
		SessionKey:     "session-dev-1",
		WorkspaceDir:   workspaceDir,
		Preset:         domain.PresetDeveloper,
		AllowedPaths:   nil, // Scope is only workspaceDir
	}
	mgr.RegisterActiveTurn(devTurn)

	// Tool outside workspace is DENIED despite developer preset
	dec, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ConversationID: "conv-dev-1",
		WorkspaceDir:   workspaceDir,
		ToolName:       "view_file",
		Args:           map[string]interface{}{"TargetFile": filepath.Join(forbiddenDir, "secret.txt")},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision, "developer preset must NOT bypass workspace jail without explicit allowed_paths")

	// Command with Cwd outside workspace is DENIED despite developer preset
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ConversationID: "conv-dev-1",
		WorkspaceDir:   workspaceDir,
		ToolName:       "run_command",
		Args:           map[string]interface{}{"CommandLine": "ls -la", "Cwd": forbiddenDir},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision, "Cwd outside workspace must be denied despite developer preset")

	// Command with Cwd inside workspace is ALLOWED under developer preset
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ConversationID: "conv-dev-1",
		WorkspaceDir:   workspaceDir,
		ToolName:       "run_command",
		Args:           map[string]interface{}{"CommandLine": "ls -la", "Cwd": workspaceDir},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)

	mgr.UnregisterTurnByID("turn-dev-1")

	// 2. Agent with explicit AllowedPaths configured (Layer 2 Scope expansion)
	scopedTurn := domain.TurnSecurityContext{
		TurnID:         "turn-scoped-1",
		ConversationID: "conv-scoped-1",
		SessionKey:     "session-scoped-1",
		WorkspaceDir:   workspaceDir,
		Preset:         domain.PresetDeveloper,
		AllowedPaths:   []string{extraDir},
	}
	mgr.RegisterActiveTurn(scopedTurn)

	// File inside extraDir is ALLOWED
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ConversationID: "conv-scoped-1",
		WorkspaceDir:   workspaceDir,
		ToolName:       "view_file",
		Args:           map[string]interface{}{"TargetFile": filepath.Join(extraDir, "doc.txt")},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision, "File in agent's allowed_paths must be allowed")

	// Command with Cwd in extraDir is ALLOWED
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ConversationID: "conv-scoped-1",
		WorkspaceDir:   workspaceDir,
		ToolName:       "run_command",
		Args:           map[string]interface{}{"CommandLine": "ls -la", "Cwd": extraDir},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision, "Cwd in agent's allowed_paths must be allowed")

	// File in forbiddenDir (outside both workspaceDir and extraDir) is still DENIED
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ConversationID: "conv-scoped-1",
		WorkspaceDir:   workspaceDir,
		ToolName:       "view_file",
		Args:           map[string]interface{}{"TargetFile": filepath.Join(forbiddenDir, "forbidden.txt")},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision, "File outside both workspace and allowed_paths must be denied")

	mgr.UnregisterTurnByID("turn-scoped-1")
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
		subj := p.agentName
		if p.preset == domain.PresetUnrestricted {
			subj = "admin"
		}
		mgr.RegisterActiveTurn(domain.TurnSecurityContext{
			ConversationID: p.convID,
			SessionKey:     "session-" + p.agentName,
			WorkspaceDir:   p.wsDir,
			Preset:         p.preset,
			AgentName:      p.agentName,
			Principal:      domain.Principal{SubjectID: subj, Kind: domain.PrincipalUser},
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
		TurnID:         "turn-ipc-unres",
		ConversationID: "conv-ipc-unres",
		SessionKey:     "session-admin",
		WorkspaceDir:   "/tmp/ws_admin",
		Preset:         domain.PresetUnrestricted,
		AgentName:      "admin_bot",
	})
	mgr.RegisterActiveTurn(domain.TurnSecurityContext{
		TurnID:         "turn-ipc-strict",
		ConversationID: "conv-ipc-strict",
		SessionKey:     "session-auditor",
		WorkspaceDir:   "/tmp/ws_audit",
		Preset:         domain.PresetStrict,
		AgentName:      "audit_bot",
	})

	client := ipc.NewClient(addr)

	// 1. Real TCP IPC Request for Unrestricted Agent
	resp1, err := client.SendHookRequest(ipc.HookRequest{
		TurnID:         "turn-ipc-unres",
		HookType:       "pre",
		ConversationID: "conv-ipc-unres",
		WorkspacePaths: []string{"/tmp/ws_admin"},
		ToolCall: ipc.HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "python custom_deploy.py"},
		},
		StepIdx: 1,
	}, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "allow", resp1.Decision, "Real IPC call for unrestricted agent must return allow")

	// 2. Real TCP IPC Request for Strict Agent (Unwhitelisted command)
	resp2, err := client.SendHookRequest(ipc.HookRequest{
		TurnID:         "turn-ipc-strict",
		HookType:       "pre",
		ConversationID: "conv-ipc-strict",
		WorkspacePaths: []string{"/tmp/ws_audit"},
		ToolCall: ipc.HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "python custom_deploy.py"},
		},
		StepIdx: 2,
	}, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "deny", resp2.Decision, "Real IPC call for strict agent must return deny")
	assert.Contains(t, resp2.Reason, "Strict")

	// 3. Real TCP IPC Request for Strict Agent (Whitelisted command)
	resp3, err := client.SendHookRequest(ipc.HookRequest{
		TurnID:         "turn-ipc-strict",
		HookType:       "pre",
		ConversationID: "conv-ipc-strict",
		WorkspacePaths: []string{"/tmp/ws_audit"},
		ToolCall: ipc.HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "go test ./..."},
		},
		StepIdx: 3,
	}, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "allow", resp3.Decision, "Real IPC call for strict agent with whitelisted cmd must return allow")
}

func TestExtractBaseCommand(t *testing.T) {
	assert.Equal(t, "python3", ExtractBaseCommand("python3 scripts/radar.py --page 1"))
	assert.Equal(t, "python3", ExtractBaseCommand("/usr/bin/python3 -c \"import camoufox; print(1)\""))
	assert.Equal(t, "node", ExtractBaseCommand("ENV_VAR=test /opt/homebrew/bin/node index.js"))
	assert.Equal(t, "git", ExtractBaseCommand("git.exe commit -m 'test'"))
	assert.Equal(t, "curl", ExtractBaseCommand("curl -s https://example.com"))
	assert.Equal(t, "", ExtractBaseCommand(""))
}

func TestManager_SessionGrants_LifecycleAndInvalidation(t *testing.T) {
	cfg := config.GetEffectiveSecurityPreset("balanced")
	mockHITL := &mockHITLApprovalPort{}
	mgr := NewManager(cfg, mockHITL, nil)
	ctx := context.Background()
	sessionKey := "telegram:123:456:0:wife_assistant"

	// 1. Initial sensitive command triggers HITL
	hitlCalls := 0
	mockHITL.requestApprovalFn = func(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
		hitlCalls++
		return domain.ApprovalDecision{Approved: true, Action: domain.ActionAllowSession}, nil
	}

	dec, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		SessionKey: sessionKey,
		ToolName:   "run_command",
		Args:       map[string]interface{}{"CommandLine": `python3 -c "import camoufox; print('page 1')"`},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)
	assert.Equal(t, 1, hitlCalls)

	// 2. Next command with DIFFERENT arguments uses the base command session grant ('python3') without triggering HITL
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		SessionKey: sessionKey,
		ToolName:   "run_command",
		Args:       map[string]interface{}{"CommandLine": `python3 -c "import camoufox; print('page 2')"`},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)
	assert.Equal(t, 1, hitlCalls, "Should reuse base command session grant without asking user again")

	// 3. Different binary ('curl') still triggers HITL
	mockHITL.requestApprovalFn = func(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
		hitlCalls++
		return domain.ApprovalDecision{Approved: true, Action: domain.ActionAllowSession}, nil
	}
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		SessionKey: sessionKey,
		ToolName:   "run_command",
		Args:       map[string]interface{}{"CommandLine": "curl -s https://api.etsy.com/v3"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)
	assert.Equal(t, 2, hitlCalls)

	// 4. Another 'curl' command uses base command grant without asking again
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		SessionKey: sessionKey,
		ToolName:   "run_command",
		Args:       map[string]interface{}{"CommandLine": "curl -s https://api.github.com/zen"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)
	assert.Equal(t, 2, hitlCalls, "Base command grant must allow same binary without HITL")

	// 5. Inviolable Hard Guardrails check: Self-escalation and destructive commands are STILL BLOCKED despite session grant!
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		SessionKey: sessionKey,
		ToolName:   "run_command",
		Args:       map[string]interface{}{"CommandLine": "rm -rf /"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision, "Destructive commands must remain denied")

	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		SessionKey: sessionKey,
		ToolName:   "run_command",
		Args:       map[string]interface{}{"CommandLine": "pkill agyent"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision, "Self-escalation tampering must remain denied")

	// 6. Test ClearSessionGrants: Invalidation clears all grants for the session
	mgr.ClearSessionGrants(sessionKey)

	mockHITL.requestApprovalFn = func(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
		hitlCalls++
		return domain.ApprovalDecision{Approved: true, Action: domain.ActionAllowOnce}, nil
	}

	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		SessionKey: sessionKey,
		ToolName:   "run_command",
		Args:       map[string]interface{}{"CommandLine": `python3 -c "import camoufox"`},
	})
	require.NoError(t, err)
	assert.Equal(t, 3, hitlCalls, "After ClearSessionGrants, sensitive command must trigger HITL again")
}

func TestManager_SessionGrants_AllowAllSession_Wildcard(t *testing.T) {
	cfg := config.GetEffectiveSecurityPreset("balanced")
	mockHITL := &mockHITLApprovalPort{}
	mgr := NewManager(cfg, mockHITL, nil)
	ctx := context.Background()
	sessionKey := "telegram:123:456:0:test_agent"

	assert.False(t, mgr.HasWildcardGrant(sessionKey))

	// 1. Initial command triggers HITL and user approves with AllowAllSession
	hitlCalls := 0
	mockHITL.requestApprovalFn = func(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
		hitlCalls++
		return domain.ApprovalDecision{Approved: true, Action: domain.ActionAllowAllSession}, nil
	}

	dec, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		SessionKey: sessionKey,
		ToolName:   "run_command",
		Args:       map[string]interface{}{"CommandLine": "curl -s https://api.github.com"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)
	assert.Equal(t, 1, hitlCalls)
	assert.True(t, mgr.HasWildcardGrant(sessionKey), "Wildcard grant must be active after ActionAllowAllSession")

	// 2. Subsequent commands with completely different binaries must execute without HITL
	for _, cmd := range []string{
		`python3 -c "import camoufox"`,
		"pip install requests",
		"git push origin main",
		"npm install -g typescript",
		"docker run alpine echo hi",
	} {
		dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
			SessionKey: sessionKey,
			ToolName:   "run_command",
			Args:       map[string]interface{}{"CommandLine": cmd},
		})
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionAllow, dec.Decision, "Command %s must be auto-approved by wildcard grant", cmd)
		assert.Equal(t, 1, hitlCalls, "HITL must NOT be called for %s", cmd)
	}

	// 3. Dashboard summary must show wildcard session grant
	dashboard := mgr.GetDashboardSummary(sessionKey)
	assert.Contains(t, dashboard.AllowedCommands, "* (wildcard session grant)")

	// 4. Inviolable hard guardrails MUST remain blocked despite wildcard grant
	for _, badCmd := range []string{
		"rm -rf /",
		"pkill agyent",
		"pkill -9 agyent",
	} {
		dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
			SessionKey: sessionKey,
			ToolName:   "run_command",
			Args:       map[string]interface{}{"CommandLine": badCmd},
		})
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision, "Hard guardrail must deny %s despite wildcard grant", badCmd)
	}

	// 5. Invalidation clears wildcard grant
	mgr.ClearSessionGrants(sessionKey)
	assert.False(t, mgr.HasWildcardGrant(sessionKey))

	// 6. After clear, sensitive command triggers HITL again
	mockHITL.requestApprovalFn = func(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
		hitlCalls++
		return domain.ApprovalDecision{Approved: true, Action: domain.ActionAllowOnce}, nil
	}
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		SessionKey: sessionKey,
		ToolName:   "run_command",
		Args:       map[string]interface{}{"CommandLine": "curl -s https://example.com"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)
	assert.Equal(t, 2, hitlCalls, "Should trigger HITL again after ClearSessionGrants")

	// 7. Test GrantSessionPermission with "all" and "all_session" aliases
	mgr.GrantSessionPermission(sessionKey, "all")
	assert.True(t, mgr.HasWildcardGrant(sessionKey), "GrantSessionPermission with 'all' must activate wildcard grant")
	mgr.ClearSessionGrants(sessionKey)

	mgr.GrantSessionPermission(sessionKey, "all_session")
	assert.True(t, mgr.HasWildcardGrant(sessionKey), "GrantSessionPermission with 'all_session' must activate wildcard grant")
	mgr.ClearSessionGrants(sessionKey)

	mgr.GrantSessionPermission(sessionKey, "*")
	assert.True(t, mgr.HasWildcardGrant(sessionKey), "GrantSessionPermission with '*' must activate wildcard grant")
}

func TestSecurityManager_WorkspaceOnlyPreset(t *testing.T) {
	tempWS := t.TempDir()
	cfg := config.GetEffectiveSecurityPreset("workspace_only")
	mockHITL := &mockHITLApprovalPort{}
	mgr := NewManager(cfg, mockHITL, nil)
	ctx := context.Background()

	// 1. File write inside workspace -> Allowed
	wsFile := filepath.Join(tempWS, "index.js")
	dec, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:     "write_to_file",
		WorkspaceDir: tempWS,
		Args:         map[string]interface{}{"TargetFile": wsFile},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)

	// 2. File write outside workspace -> Denied by PathJail
	outerFile := "/etc/hosts"
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:     "write_to_file",
		WorkspaceDir: tempWS,
		Args:         map[string]interface{}{"TargetFile": outerFile},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Path Jail")

	// 3. Command inside workspace with Cwd inside workspace -> Allowed
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:     "run_command",
		WorkspaceDir: tempWS,
		Args: map[string]interface{}{
			"CommandLine": "git status",
			"Cwd":         tempWS,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)

	// 4. Command with Cwd outside workspace -> Denied by PathJail
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:     "run_command",
		WorkspaceDir: tempWS,
		Args: map[string]interface{}{
			"CommandLine": "ls -la",
			"Cwd":         "/tmp",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "outside active workspace")

	// 5. Destructive / blacklisted command inside workspace -> Denied
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:     "run_command",
		WorkspaceDir: tempWS,
		Args: map[string]interface{}{
			"CommandLine": "rm -rf /",
			"Cwd":         tempWS,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
}

func TestSecurityManager_ScriptPreInspectionAndBase64Deobfuscation(t *testing.T) {
	tempWS := t.TempDir()
	cfg := config.GetEffectiveSecurityPreset("workspace_only")
	mockHITL := &mockHITLApprovalPort{}
	mgr := NewManager(cfg, mockHITL, nil)
	ctx := context.Background()

	// 1. Script containing plaintext forbidden path reference (e.g. ~/.ssh/id_rsa) -> Denied
	plainScript := filepath.Join(tempWS, "attack_plain.py")
	err := os.WriteFile(plainScript, []byte(`
with open("/Users/victim/.ssh/id_rsa", "r") as f:
    print(f.read())
`), 0644)
	require.NoError(t, err)

	dec, err := mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:     "run_command",
		WorkspaceDir: tempWS,
		Args: map[string]interface{}{
			"CommandLine": "python3 attack_plain.py",
			"Cwd":         tempWS,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Script Pre-Inspection")
	assert.Contains(t, dec.Reason, ".ssh")

	// 2. JavaScript file with Base64-obfuscated Buffer.from payload pointing to /etc/shadow -> Denied
	b64Payload := "L2V0Yy9zaGFkb3c=" // base64 for "/etc/shadow"
	jsScript := filepath.Join(tempWS, "attack_b64.js")
	err = os.WriteFile(jsScript, []byte(fmt.Sprintf(`
const fs = require('fs');
const path = Buffer.from("%s", "base64").toString();
console.log(fs.readFileSync(path));
`, b64Payload)), 0644)
	require.NoError(t, err)

	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:     "run_command",
		WorkspaceDir: tempWS,
		Args: map[string]interface{}{
			"CommandLine": "node attack_b64.js",
			"Cwd":         tempWS,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Obfuscation Detection")
	assert.Contains(t, dec.Reason, "/etc/shadow")

	// 3. Inline shell command with base64 decoded destructive payload (rm -rf /) -> Denied
	rmB64 := "cm0gLXJmIC8=" // base64 for "rm -rf /"
	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:     "run_command",
		WorkspaceDir: tempWS,
		Args: map[string]interface{}{
			"CommandLine": fmt.Sprintf("echo '%s' | base64 -d | sh", rmB64),
			"Cwd":         tempWS,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision)
	assert.Contains(t, dec.Reason, "Obfuscation Detection")

	// 4. Safe legitimate script (e.g. hello world / workspace operations) -> Allowed
	safeScript := filepath.Join(tempWS, "safe.py")
	err = os.WriteFile(safeScript, []byte(`
import os
print("Hello from workspace agent!")
`), 0644)
	require.NoError(t, err)

	dec, err = mgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
		ToolName:     "run_command",
		WorkspaceDir: tempWS,
		Args: map[string]interface{}{
			"CommandLine": "python3 safe.py",
			"Cwd":         tempWS,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)
}

func TestSecurityManager_SessionGrantCannotBypassControlPlaneProtection(t *testing.T) {
	cfg := config.GetEffectiveSecurityPreset("balanced")
	mockHITL := &mockHITLApprovalPort{}
	mgr := NewManager(cfg, mockHITL, nil)
	ctx := context.Background()

	sessionKey := "telegram:test-session-grants"
	// Grant permission for "cat"
	mgr.GrantSessionPermission(sessionKey, "cat")

	// 1. Normal cat command within workspace -> Allowed by session grant
	dec, err := mgr.EvaluateCommand(ctx, sessionKey, "developer", "cat MEMORY.md")
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, dec.Decision)

	// 2. cat targeting control plane paths (e.g. /etc/shadow or .agents/hooks.json or ~/.ssh/id_rsa) -> MUST BE DENIED!
	for _, evilCmd := range []string{
		"cat /etc/shadow",
		"cat ~/.ssh/id_rsa",
		"cat .agents/hooks.json",
		"cat ~/.agyent/config.yaml",
	} {
		dec, err = mgr.EvaluateCommand(ctx, sessionKey, "developer", evilCmd)
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision, "Command '%s' must be denied despite session grant", evilCmd)
		assert.True(t, dec.Decision == domain.DecisionDeny)
	}
}

