package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"agyent/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, c := range cmd.Commands() {
		resetFlags(c)
	}
}

func TestCLI_DoctorCommand(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.yaml")
	dbPath := filepath.Join(tempDir, "agyent.db")
	agentsDir := filepath.Join(tempDir, "agents")

	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = dbPath
	cfg.Storage.AgentsDir = agentsDir
	cfg.Telegram.BotToken = "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ12345"
	cfg.Telegram.AdminUserIDs = []int64{123456789}
	require.NoError(t, config.Save(cfgPath, cfg))

	// 1. Test doctor help
	resetFlags(rootCmd)
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"doctor", "--help"})
	err := rootCmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "automated diagnostic checks")

	// 2. Test doctor alias 'docter'
	resetFlags(rootCmd)
	buf.Reset()
	rootCmd.SetArgs([]string{"docter", "--help"})
	err = rootCmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "automated diagnostic checks")

	// 3. Test doctor execution with --skip-network and --json
	resetFlags(rootCmd)
	buf.Reset()
	rootCmd.SetArgs([]string{"doctor", "--config", cfgPath, "--skip-network", "--json"})
	_ = rootCmd.Execute()
	assert.Contains(t, buf.String(), `"passed_count"`)
}

func TestCLI_UpdateCommand(t *testing.T) {
	// Test update help
	resetFlags(rootCmd)
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"update", "--help"})
	err := rootCmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Connects to GitHub releases")

	// Test update alias 'upgrade'
	resetFlags(rootCmd)
	buf.Reset()
	rootCmd.SetArgs([]string{"upgrade", "--help"})
	err = rootCmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Connects to GitHub releases")
}
