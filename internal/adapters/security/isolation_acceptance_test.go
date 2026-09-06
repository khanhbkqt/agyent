package security_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agyent/internal/adapters/security"
	"agyent/internal/adapters/security/pathjail"
	"agyent/internal/config"
	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAcceptancePOCMatrix validates all rows of the Acceptance POC Matrix defined in
// docs/agy-project-scoped-security-isolation.md.
func TestAcceptancePOCMatrix(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	workspaceDir := filepath.Join(tempDir, "agent_workspace")
	outsideDir := filepath.Join(tempDir, "host_sensitive")
	require.NoError(t, os.MkdirAll(workspaceDir, 0755))
	require.NoError(t, os.MkdirAll(outsideDir, 0755))

	// Provision dummy canary files
	hostSecretFile := filepath.Join(outsideDir, "credentials.json")
	require.NoError(t, os.WriteFile(hostSecretFile, []byte("SUPER_SECRET_KEY=123"), 0600))

	cfg := config.SecurityConfig{
		Preset: "balanced",
		Filesystem: config.FilesystemGuardrailConfig{
			EnforceWorkspaceJail: true,
			AllowedPaths:         []string{workspaceDir},
			ForbiddenPaths:       []string{outsideDir, "~/.ssh", "~/.agyent/config.yaml"},
		},
		Commands: config.CommandGuardrailConfig{
			CustomBlacklist: []string{`(?i)\bformat\b`, `(?i)\brm\s+-rf\s+/`},
			CustomWhitelist: []string{`^git$`, `^go$`},
		},
	}
	secMgr := security.NewManager(cfg, nil, nil)

	// ROW 1: Project Selection & Execution Admission
	t.Run("POC_Row1_ProjectSelectionAndAdmissionBinding", func(t *testing.T) {
		turnID := "turn-poc-101"
		sessionKey := "telegram:user-123"

		secMgr.RegisterActiveTurn(domain.TurnSecurityContext{
			TurnID:       turnID,
			SessionKey:   sessionKey,
			WorkspaceDir: workspaceDir,
			AgentName:    "agent-poc",
			Preset:       domain.PresetBalanced,
		})
		defer secMgr.UnregisterTurnByID(turnID)

		turnCtx, ok := secMgr.ResolveTurnByID(turnID)
		require.True(t, ok)
		assert.Equal(t, sessionKey, turnCtx.SessionKey)
		assert.Equal(t, workspaceDir, turnCtx.WorkspaceDir)

		// Stale or unregistered turn ID fails closed
		_, ok = secMgr.ResolveTurnByID("turn-invalid-999")
		assert.False(t, ok, "unregistered turn ID must fail closed")
	})

	// ROW 2: Filesystem Jail & Canonicalization
	t.Run("POC_Row2_FilesystemJailAndSymlinkEscapePrevention", func(t *testing.T) {
		pjEval := pathjail.NewEvaluator(cfg.Filesystem, nil)

		// 1. In-workspace access allowed
		validFile := filepath.Join(workspaceDir, "src", "code.go")
		dec, err := pjEval.EvaluatePath(workspaceDir, validFile, false, false)
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionAllow, dec.Decision)

		// 2. Traversal attempt denied
		traversalFile := filepath.Join(workspaceDir, "..", "host_sensitive", "credentials.json")
		dec, err = pjEval.EvaluatePath(workspaceDir, traversalFile, false, false)
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision)

		// 3. Symlink pointing outside workspace denied
		symlinkTarget := filepath.Join(workspaceDir, "symlink_escape")
		_ = os.Symlink(outsideDir, symlinkTarget)
		dec, err = pjEval.EvaluatePath(workspaceDir, filepath.Join(symlinkTarget, "credentials.json"), false, false)
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision)
	})

	// ROW 3: Control Plane Protection
	t.Run("POC_Row3_ControlPlaneAndHookTamperingPrevention", func(t *testing.T) {
		// Ensure hook provisioning creates atomic .agents/hooks.json
		hookPath, err := security.EnsureWorkspaceHooksProvisioned(workspaceDir, "", nil)
		require.NoError(t, err)
		assert.FileExists(t, hookPath)

		// Attempt by agent to modify .agents/hooks.json must be strictly denied
		pjEval := pathjail.NewEvaluator(cfg.Filesystem, nil)
		dec, err := pjEval.EvaluatePath(workspaceDir, hookPath, true, false)
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision)
		assert.Contains(t, dec.Reason, "Control Plane Protection")

		// Staged privilege escalation via code write to gateway config/db must be blocked
		dec, err = secMgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
			ToolName:     "write_to_file",
			WorkspaceDir: workspaceDir,
			Args: map[string]interface{}{
				"TargetFile":  filepath.Join(workspaceDir, "escalate.sh"),
				"CodeContent": "sqlite3 ~/.agyent/agyent.db 'UPDATE users SET is_admin=1'",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision)
		assert.Contains(t, dec.Reason, "Privilege Escalation Blocked")
	})

	// ROW 4: Command Policy AST & Pipeline Validation
	t.Run("POC_Row4_CommandPolicyAndShellInjectionPrevention", func(t *testing.T) {
		// Chained destructive command
		dec, err := secMgr.EvaluateCommand(ctx, "test-sess", "developer", "git status && rm -rf /")
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision)

		// Subshell interpreter injection
		dec, err = secMgr.EvaluateCommand(ctx, "test-sess", "developer", "sh -c 'pkill agyent'")
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision)

		// Working directory outside workspace in run_command tool call
		dec, err = secMgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
			ToolName:     "run_command",
			WorkspaceDir: workspaceDir,
			Args: map[string]interface{}{
				"CommandLine": "ls",
				"Cwd":         outsideDir,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision)
		assert.Contains(t, dec.Reason, "outside active workspace")
	})

	// ROW 5: Capability Matrix & Default-Deny for Unknown Tools
	t.Run("POC_Row5_CapabilityMatrixAndUnknownToolDefaultDeny", func(t *testing.T) {
		// 1. Unknown tool -> Fail closed
		dec, err := secMgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
			ToolName:     "unregistered_alien_tool",
			WorkspaceDir: workspaceDir,
			Args:         map[string]interface{}{"foo": "bar"},
		})
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision)
		assert.Contains(t, dec.Reason, "Unknown or unregistered tool")

		// 2. SSRF / Cloud Metadata network access -> Denied
		dec, err = secMgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
			ToolName: "read_url_content",
			Args:     map[string]interface{}{"Url": "http://169.254.169.254/latest/meta-data/"},
		})
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision)
		assert.Contains(t, dec.Reason, "Metadata")

		// 3. Localhost loopback egress -> Denied
		dec, err = secMgr.EvaluateToolCall(ctx, domain.ToolEvaluationRequest{
			ToolName: "read_url_content",
			Args:     map[string]interface{}{"Url": "http://127.0.0.1:8080/admin"},
		})
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, dec.Decision)
	})

	// ROW 6: Rollback & Clean Lifecycle Invalidation
	t.Run("POC_Row6_SessionInvalidationAndCleanup", func(t *testing.T) {
		sessionKey := "telegram:session-cleanup-test"

		secMgr.GrantSessionPermission(sessionKey, "git")
		secMgr.ClearSessionGrants(sessionKey)

		// After clearing grants, sensitive commands re-trigger HITL or policy evaluation
		dec, err := secMgr.EvaluateCommand(ctx, sessionKey, "user", "curl https://example.com")
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionAsk, dec.Decision)
	})
}
