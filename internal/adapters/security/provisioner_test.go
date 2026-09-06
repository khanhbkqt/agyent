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

func TestEnsureAGYProjectProvisioned(t *testing.T) {
	ws := t.TempDir()
	projID := "agy-proj-unit-test"

	err := EnsureAGYProjectProvisioned(projID, "unit_test_agent", ws, nil)
	require.NoError(t, err)

	projectsDir, err := filepath.Abs(filepath.Join(os.Getenv("HOME"), ".gemini/config/projects"))
	require.NoError(t, err)
	filePath := filepath.Join(projectsDir, projID+".json")
	defer func() { _ = os.Remove(filePath) }()

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), projID)
	assert.Contains(t, string(data), "CASCADE_COMMANDS_AUTO_EXECUTION_EAGER")
	assert.Contains(t, string(data), ws)
	assert.Contains(t, string(data), "command")
}
