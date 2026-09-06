package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"agyent/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecurityCLI(t *testing.T) {
	t.Setenv("AGYENT_TURN_ID", "")
	tempDir := t.TempDir()
	tempCfgPath := filepath.Join(tempDir, "config.yaml")
	tempDBPath := filepath.Join(tempDir, "agyent.db")

	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = tempDBPath
	cfg.Security.Preset = "balanced"
	require.NoError(t, config.Save(tempCfgPath, cfg))

	t.Run("Security Status Command", func(t *testing.T) {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs([]string{"security", "status", "-c", tempCfgPath})

		err := rootCmd.Execute()
		require.NoError(t, err)

		out := buf.String()
		assert.Contains(t, out, "Agyent Security Gateway Status")
		assert.Contains(t, out, "Global Security Preset")
		assert.Contains(t, out, "balanced")
	})

	t.Run("Security List Presets Command", func(t *testing.T) {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs([]string{"security", "list-presets", "-c", tempCfgPath})

		err := rootCmd.Execute()
		require.NoError(t, err)

		out := buf.String()
		assert.Contains(t, out, "Agyent Security Presets Matrix")
		assert.Contains(t, out, "unrestricted")
		assert.Contains(t, out, "developer")
		assert.Contains(t, out, "balanced")
		assert.Contains(t, out, "strict")
		assert.Contains(t, out, "read_only")
	})

	t.Run("Security Preset Show Default", func(t *testing.T) {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs([]string{"security", "preset", "-c", tempCfgPath})

		err := rootCmd.Execute()
		require.NoError(t, err)

		out := buf.String()
		assert.Contains(t, out, "Current global security preset")
		assert.Contains(t, out, "balanced")
	})

	t.Run("Security Preset Downgrade to Unrestricted (Allowed via CLI)", func(t *testing.T) {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		secAgentFlag = ""
		rootCmd.SetArgs([]string{"security", "preset", "unrestricted", "-c", tempCfgPath})

		err := rootCmd.Execute()
		require.NoError(t, err)

		out := buf.String()
		assert.Contains(t, out, "Security Preset Updated Successfully")
		assert.Contains(t, out, "unrestricted")

		// Reload config and verify
		reloaded, err := config.Load(tempCfgPath)
		require.NoError(t, err)
		assert.Equal(t, "unrestricted", reloaded.Security.Preset)
		assert.Equal(t, "unrestricted", reloaded.Agents["agyent"].SecurityPreset)
	})

	t.Run("Security Preset Set Specific Agent", func(t *testing.T) {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs([]string{"security", "preset", "strict", "--agent", "auditor", "-c", tempCfgPath})

		err := rootCmd.Execute()
		require.NoError(t, err)

		out := buf.String()
		assert.Contains(t, out, "Target Agent")
		assert.Contains(t, out, "auditor")
		assert.Contains(t, out, "strict")

		reloaded, err := config.Load(tempCfgPath)
		require.NoError(t, err)
		assert.Equal(t, "strict", reloaded.Agents["auditor"].SecurityPreset)
	})

	t.Run("Security Preset Invalid Option", func(t *testing.T) {
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs([]string{"security", "preset", "super_unsafe", "-c", tempCfgPath})

		err := rootCmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid security preset")
	})

	t.Run("Security Preset Blocked During Agent Turn (Anti-Self-Escalation)", func(t *testing.T) {
		t.Setenv("AGYENT_TURN_ID", "turn-in-flight-12345")
		buf := new(bytes.Buffer)
		rootCmd.SetOut(buf)
		rootCmd.SetErr(buf)
		rootCmd.SetArgs([]string{"security", "preset", "unrestricted", "-c", tempCfgPath})

		err := rootCmd.Execute()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Self-privilege escalation is strictly forbidden")
	})
}

