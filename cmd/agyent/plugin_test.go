package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginCLI_List(t *testing.T) {
	buf := new(bytes.Buffer)
	pluginListCmd.SetOut(buf)
	pluginListCmd.SetErr(buf)

	err := pluginListCmd.RunE(pluginListCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "VERSION")
	assert.Contains(t, out, "EMBEDDED")
	assert.Contains(t, out, "browser-camoufox")
	assert.Contains(t, out, "database-sqlite")
	assert.Contains(t, out, "system-diagnostics")
	assert.Contains(t, out, "subagent-dispatcher")
}

func TestPluginCLI_Update(t *testing.T) {
	tempDir := t.TempDir()
	pluginTargetDirFlag = tempDir
	pluginForceFlag = true

	buf := new(bytes.Buffer)
	pluginUpdateCmd.SetOut(buf)
	pluginUpdateCmd.SetErr(buf)

	err := pluginUpdateCmd.RunE(pluginUpdateCmd, []string{})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Synchronizing embedded plugins")
	assert.Contains(t, out, "browser-camoufox")
	assert.Contains(t, out, "database-sqlite")
	assert.Contains(t, out, "Plugin synchronization complete")

	// Verify files extracted to tempDir
	assert.DirExists(t, filepath.Join(tempDir, "browser-camoufox"))
	assert.FileExists(t, filepath.Join(tempDir, "browser-camoufox", "plugin.json"))
	assert.DirExists(t, filepath.Join(tempDir, "database-sqlite"))
	assert.FileExists(t, filepath.Join(tempDir, "database-sqlite", "plugin.json"))

	// Test single plugin update
	bufSingle := new(bytes.Buffer)
	pluginUpdateCmd.SetOut(bufSingle)
	pluginUpdateCmd.SetErr(bufSingle)

	errSingle := pluginUpdateCmd.RunE(pluginUpdateCmd, []string{"database-sqlite"})
	require.NoError(t, errSingle)
	assert.Contains(t, bufSingle.String(), "Plugin \"database-sqlite\" updated successfully")
}

func TestPluginCLI_Install(t *testing.T) {
	tempDir := t.TempDir()
	origHome := os.Getenv("USERPROFILE")
	os.Setenv("USERPROFILE", tempDir)
	defer os.Setenv("USERPROFILE", origHome)

	buf := new(bytes.Buffer)
	pluginInstallCmd.SetOut(buf)
	pluginInstallCmd.SetErr(buf)

	err := pluginInstallCmd.RunE(pluginInstallCmd, []string{"system-diagnostics"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Successfully installed plugin \"system-diagnostics\"")
	assert.DirExists(t, filepath.Join(tempDir, ".agyent", "plugins", "system-diagnostics"))
}
