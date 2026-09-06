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
	require.NoError(t, os.MkdirAll(workspaceDir, 0755))

	cfg := config.FilesystemGuardrailConfig{
		EnforceWorkspaceJail: true,
		AllowedPaths:         []string{workspaceDir},
	}
	evaluator := NewEvaluator(cfg, nil)

	// Test case variations on control-plane writes (.Agents, .AGYENT)
	for _, target := range []string{
		filepath.Join(workspaceDir, ".Agents", "hooks.json"),
		filepath.Join(workspaceDir, ".AGYENT", "config.yaml"),
		filepath.Join(workspaceDir, ".agents", "rules.md"),
		filepath.Join(workspaceDir, ".agyent", "settings.json"),
	} {
		decision, err := evaluator.EvaluatePath(workspaceDir, target, true, false)
		require.NoError(t, err)
		assert.Equal(t, domain.DecisionDeny, decision.Decision, "control-plane writes must be denied regardless of casing")
		assert.Contains(t, decision.Reason, "Control Plane Protection")
	}
}

func TestPathJail_RelativeAllowedPaths_NoDaemonCWDLeak(t *testing.T) {
	cfg := config.FilesystemGuardrailConfig{
		EnforceWorkspaceJail: true,
		AllowedPaths:         []string{"."},
	}
	evaluator := NewEvaluator(cfg, nil)
	assert.Empty(t, evaluator.AllowedPaths(), "relative '.' should not bind daemon CWD to allowedPaths")
}
