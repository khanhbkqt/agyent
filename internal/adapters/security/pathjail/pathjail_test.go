package pathjail

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathJail_WorkspaceEnforcement(t *testing.T) {
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "workspace")
	outsideDir := filepath.Join(tempDir, "outside")

	require.NoError(t, os.MkdirAll(workspaceDir, 0755))
	require.NoError(t, os.MkdirAll(outsideDir, 0755))

	cfg := config.FilesystemGuardrailConfig{
		EnforceWorkspaceJail: true,
		AllowedPaths:         []string{workspaceDir},
		ForbiddenPaths:       []string{"~/.ssh", "~/.aws", filepath.Join(workspaceDir, "secret_forbidden.key")},
	}

	evaluator := NewEvaluator(cfg, []string{".env", "~/.agyent/config.yaml"})

	// Test 1: Allowed file inside workspace
	decision, err := evaluator.EvaluatePath(workspaceDir, filepath.Join(workspaceDir, "src", "main.go"), false, false)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, decision.Decision)

	// Test 2: Denied file outside workspace
	decision, err = evaluator.EvaluatePath(workspaceDir, filepath.Join(outsideDir, "leak.txt"), false, false)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "resides outside active workspace")

	// Test 3: Blacklisted path inside workspace
	decision, err = evaluator.EvaluatePath(workspaceDir, filepath.Join(workspaceDir, "secret_forbidden.key"), false, false)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "Access strictly forbidden")

	// Test 4: Delegated config file outside workspace (HITL Ask)
	decision, err = evaluator.EvaluatePath(workspaceDir, filepath.Join(outsideDir, ".env"), true, true)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAsk, decision.Decision)
	assert.Contains(t, decision.Reason, "requires user confirmation")

	// Test 5: Control plane path write inside workspace (.agents/hooks.json) -> Strictly Denied
	decision, err = evaluator.EvaluatePath(workspaceDir, filepath.Join(workspaceDir, ".agents", "hooks.json"), true, false)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "Control Plane Protection")
}

func TestPathJail_WindowsQuirks(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping Windows quirks test on non-windows platform")
	}

	tempDir := t.TempDir()
	cfg := config.FilesystemGuardrailConfig{
		EnforceWorkspaceJail: true,
		AllowedPaths:         []string{tempDir},
	}
	evaluator := NewEvaluator(cfg, nil)

	// Test 1: Alternate Data Stream (ADS) blocked
	decision, err := evaluator.EvaluatePath(tempDir, filepath.Join(tempDir, "file.txt:hidden.exe"), true, false)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "Alternate Data Streams")

	// Test 2: Device UNC path blocked
	decision, err = evaluator.EvaluatePath(tempDir, `\\.\C:\Windows\System32`, false, false)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "Device and loopback UNC")
}

func TestPathJail_InboundUploadsInWorkspace(t *testing.T) {
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "workspace")
	externalUploadsDir := filepath.Join(tempDir, "external_uploads")
	workspaceUploadsDir := filepath.Join(workspaceDir, "uploads")

	require.NoError(t, os.MkdirAll(workspaceUploadsDir, 0755))
	require.NoError(t, os.MkdirAll(externalUploadsDir, 0755))

	cfg := config.FilesystemGuardrailConfig{
		EnforceWorkspaceJail: true,
		AllowedPaths:         []string{workspaceDir},
	}
	evaluator := NewEvaluator(cfg, nil)

	// 1. Inbound file relocated inside workspace/uploads/ -> Allowed with 0 friction
	wsUploadFile := filepath.Join(workspaceUploadsDir, "spec_123.pdf")
	decision, err := evaluator.EvaluatePath(workspaceDir, wsUploadFile, false, false)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, decision.Decision, "in-workspace upload files must be allowed directly")

	// 2. Out-of-workspace inbound file (legacy behavior) -> Denied by PathJail
	extUploadFile := filepath.Join(externalUploadsDir, "spec_123.pdf")
	decision2, err := evaluator.EvaluatePath(workspaceDir, extUploadFile, false, false)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision2.Decision, "external upload files outside workspace must be blocked by PathJail")
}

func TestPathJail_ControlPlaneCaseVariations(t *testing.T) {
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "workspace")
	daemonDir := filepath.Join(tempDir, ".agyent")
	require.NoError(t, os.MkdirAll(workspaceDir, 0755))
	require.NoError(t, os.MkdirAll(daemonDir, 0755))

	cfg := config.FilesystemGuardrailConfig{
		EnforceWorkspaceJail: true,
		AllowedPaths:         []string{workspaceDir},
	}
	evaluator := NewEvaluator(cfg, nil)

	// Test case variations on control-plane writes (Hooks, DB, Daemon config)
	for _, target := range []string{
		filepath.Join(workspaceDir, ".Agents", "hooks.json"),
		filepath.Join(workspaceDir, ".AGENTS", "HOOKS.JSON"),
		filepath.Join(daemonDir, "config.yaml"),
		filepath.Join(daemonDir, "CONFIG.YML"),
		filepath.Join(daemonDir, "agyent.db"),
		filepath.Join(daemonDir, "agyent.db-wal"),
	} {
		decision, err := evaluator.EvaluatePath(workspaceDir, target, true, false)
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, decision.Decision, "control-plane writes must be denied for %s", target)
		assert.Contains(t, decision.Reason, "Control Plane Protection")
	}
}

func TestPathJail_AgentMemoryAndWorkspaceUnderAgyentHome(t *testing.T) {
	tempDir := t.TempDir()
	// Real-world setup: agent workspace resides at ~/.agyent/workspace or ~/.agyent/workspace-<agent>
	agentWorkspace := filepath.Join(tempDir, ".agyent", "workspace")
	require.NoError(t, os.MkdirAll(agentWorkspace, 0755))

	cfg := config.FilesystemGuardrailConfig{
		EnforceWorkspaceJail: true,
		AllowedPaths:         []string{agentWorkspace},
	}
	evaluator := NewEvaluator(cfg, []string{".env", filepath.Join(tempDir, ".agyent", "config.yaml")})

	// 1. Agent writing to MEMORY.md inside its workspace under .agyent must be ALLOWED
	for _, memoryFile := range []string{
		"MEMORY.md",
		"memory/2026-09-06.md",
		"IDENTITY.md",
		"SOUL.md",
		"USER.md",
		"AGENTS.md",
		"HEARTBEAT.md",
		"src/main.go",
	} {
		target := filepath.Join(agentWorkspace, memoryFile)
		decision, err := evaluator.EvaluatePath(agentWorkspace, target, true, false)
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionAllow, decision.Decision, "Agent write to %s inside workspace must be allowed", memoryFile)
	}

	// 2. Writing to .agents/hooks.json inside agent workspace must still be strictly DENIED
	hookTarget := filepath.Join(agentWorkspace, ".agents", "hooks.json")
	decision, err := evaluator.EvaluatePath(agentWorkspace, hookTarget, true, false)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "Control Plane Protection")

	// 3. Attempting to write to daemon database at ~/.agyent/agyent.db must be strictly DENIED
	dbTarget := filepath.Join(tempDir, ".agyent", "agyent.db")
	decision, err = evaluator.EvaluatePath(agentWorkspace, dbTarget, true, false)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "Control Plane Protection")

	// 4. Attempting to write to daemon config.yaml when in manageableFiles triggers HITL Ask in developer mode
	cfgTarget := filepath.Join(tempDir, ".agyent", "config.yaml")
	decision, err = evaluator.EvaluatePath(agentWorkspace, cfgTarget, true, true)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAsk, decision.Decision)
}

func TestPathJail_ManageableGlobMatching_And_BlacklistNoBypass(t *testing.T) {
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "workspace")
	outsideDir := filepath.Join(tempDir, "outside")
	require.NoError(t, os.MkdirAll(workspaceDir, 0755))
	require.NoError(t, os.MkdirAll(outsideDir, 0755))

	cfg := config.FilesystemGuardrailConfig{
		EnforceWorkspaceJail: true,
		AllowedPaths:         []string{workspaceDir},
		ForbiddenPaths:       []string{filepath.Join(outsideDir, "forbidden.env")},
	}
	// Manageable with glob pattern "*.env"
	evaluator := NewEvaluator(cfg, []string{"*.env", "config-*.json"})

	// 1. External file matching manageable glob "*.env" outside workspace -> triggers HITL Ask
	externalEnv := filepath.Join(outsideDir, "local.env")
	dec, err := evaluator.EvaluatePath(workspaceDir, externalEnv, true, true)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAsk, dec.Decision, "Glob-matching manageable file should trigger Ask")

	// 2. External file matching manageable glob "config-*.json" -> triggers HITL Ask
	externalConfigJSON := filepath.Join(outsideDir, "config-dev.json")
	dec, err = evaluator.EvaluatePath(workspaceDir, externalConfigJSON, true, true)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAsk, dec.Decision)

	// 3. Blacklisted file matching manageable pattern must NEVER be bypassed by isManageable -> strictly Denied
	blacklistedEnv := filepath.Join(outsideDir, "forbidden.env")
	dec, err = evaluator.EvaluatePath(workspaceDir, blacklistedEnv, true, true)
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, dec.Decision, "Forbidden blacklist must not be bypassed by manageableFiles")
	assert.Contains(t, dec.Reason, "Access strictly forbidden")
}

func TestPathJail_RelativeAllowedPaths_NoDaemonCWDLeak(t *testing.T) {
	cfg := config.FilesystemGuardrailConfig{
		EnforceWorkspaceJail: true,
		AllowedPaths:         []string{"."},
	}
	evaluator := NewEvaluator(cfg, nil)
	assert.Empty(t, evaluator.AllowedPaths(), "relative '.' should not bind daemon CWD to allowedPaths")
}
