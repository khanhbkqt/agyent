package security

import (
	"encoding/json"
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

func TestEnsureAGYProjectProvisionedIncludesWorkspacePathGrants(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	workspaceDir := filepath.Join(homeDir, "agent-workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0700))

	err := EnsureAGYProjectProvisioned("agy-proj-test", "test", workspaceDir, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(homeDir, ".gemini", "config", "projects", "agy-proj-test.json"))
	require.NoError(t, err)
	var project AGYProjectConfig
	require.NoError(t, json.Unmarshal(data, &project))

	grants := project.PermissionGrants.PermissionGrants.Allow
	canonicalWorkspace, err := filepath.EvalSymlinks(workspaceDir)
	require.NoError(t, err)
	for _, tool := range []string{"read_file", "write_file", "edit_file", "view_file", "list_dir", "grep_search", "find_by_name"} {
		assert.Contains(t, grants, tool+"("+canonicalWorkspace+")")
		assert.Contains(t, grants, tool+"("+filepath.Join(canonicalWorkspace, "**")+")")
	}
}

func TestEnsureAGYProjectProvisionedRejectsProjectPathTraversal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := EnsureAGYProjectProvisioned("../settings", "test", t.TempDir(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid path character")
}
