package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureWorkspaceHooksProvisionedFailsClosedOnCorruptConfig(t *testing.T) {
	workspace := t.TempDir()
	agentsDir := filepath.Join(workspace, ".agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0700))
	hooksPath := filepath.Join(agentsDir, "hooks.json")
	corrupt := []byte(`{"existing-hook": `)
	require.NoError(t, os.WriteFile(hooksPath, corrupt, 0600))

	_, err := EnsureWorkspaceHooksProvisioned(workspace, "agyent", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid existing hooks configuration")

	after, readErr := os.ReadFile(hooksPath)
	require.NoError(t, readErr)
	assert.Equal(t, corrupt, after, "corrupt user configuration must not be overwritten")
}

func TestEnsureWorkspaceHooksProvisionedHandlesJSONNull(t *testing.T) {
	workspace := t.TempDir()
	agentsDir := filepath.Join(workspace, ".agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "hooks.json"), []byte("null"), 0600))

	hooksPath, err := EnsureWorkspaceHooksProvisioned(workspace, "agyent", nil)
	require.NoError(t, err)
	data, err := os.ReadFile(hooksPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "agyent-security-gate")
}

func TestEnsureWorkspaceSettingsProvisioned(t *testing.T) {
	ws := t.TempDir()
	err := EnsureWorkspaceSettingsProvisioned(ws, nil)
	require.NoError(t, err)

	agentsSettings := filepath.Join(ws, ".agents", "settings.json")
	data, err := os.ReadFile(agentsSettings)
	require.NoError(t, err)
	assert.Contains(t, string(data), "command")
	assert.Contains(t, string(data), "permissions")

	geminiSettings := filepath.Join(ws, ".gemini", "settings.json")
	geminiData, err := os.ReadFile(geminiSettings)
	require.NoError(t, err)
	assert.Contains(t, string(geminiData), "command")
}

