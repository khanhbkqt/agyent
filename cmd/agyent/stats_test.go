package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/config"
	"agyent/internal/core/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLI_StatsCommand(t *testing.T) {
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

	// Populate SQLite with mock audit logs and compacted conversations
	store, err := sqlite.Open(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Seed audit logs for agent "agyent"
	require.NoError(t, store.LogAudit(ctx, &domain.AuditLog{
		SessionKey:     "telegram:111:222",
		AgentName:      "agyent",
		ConversationID: "conv-1",
		Model:          "gemini-3.7-flash",
		Status:         "SUCCESS",
		Usage: domain.TokenUsage{
			InputTokens:     10000,
			OutputTokens:    500,
			CacheReadTokens: 8500,
			TotalTokens:     10500,
		},
		CreatedAt: time.Now(),
	}))

	// Seed audit logs for agent "coder"
	require.NoError(t, store.LogAudit(ctx, &domain.AuditLog{
		SessionKey:     "telegram:111:333",
		AgentName:      "coder",
		ConversationID: "conv-2",
		Model:          "claude-sonnet-4-6",
		Status:         "SUCCESS",
		Usage: domain.TokenUsage{
			InputTokens:     20000,
			OutputTokens:    1200,
			CacheReadTokens: 0,
			TotalTokens:     21200,
		},
		CreatedAt: time.Now(),
	}))

	// 1. Test stats --help
	resetFlags(rootCmd)
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"stats", "--help"})
	err = rootCmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "token consumption metrics")
	assert.Contains(t, buf.String(), "--agent")
	assert.Contains(t, buf.String(), "--session")

	// 2. Test global stats
	resetFlags(rootCmd)
	buf.Reset()
	rootCmd.SetArgs([]string{"--config", cfgPath, "stats"})
	err = rootCmd.Execute()
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "AGYENT TOKEN ANALYTICS & EFFICIENCY DASHBOARD")
	assert.Contains(t, out, "TODAY'S CONSUMPTION")
	assert.Contains(t, out, "ALL-TIME LIFETIME")
	assert.Contains(t, out, "BREAKDOWN BY AGENT PERSONA")
	assert.Contains(t, out, "agyent")
	assert.Contains(t, out, "coder")
	assert.Contains(t, out, "BREAKDOWN BY AI MODEL")
	assert.Contains(t, out, "Gemini 3.7 Flash")
	assert.Contains(t, out, "Claude Sonnet 4.6")

	// 3. Test stats with agent filter: agyent stats --agent agyent
	resetFlags(rootCmd)
	buf.Reset()
	rootCmd.SetArgs([]string{"--config", cfgPath, "stats", "--agent", "agyent"})
	err = rootCmd.Execute()
	require.NoError(t, err)
	outAgent := buf.String()
	assert.Contains(t, outAgent, "Filtered Agent: [agyent]")
	assert.Contains(t, outAgent, "10,500 tokens")

	// 4. Test stats with JSON output: agyent stats --json
	resetFlags(rootCmd)
	buf.Reset()
	rootCmd.SetArgs([]string{"--config", cfgPath, "stats", "--json"})
	err = rootCmd.Execute()
	require.NoError(t, err)
	outJSON := buf.String()
	assert.Contains(t, outJSON, `"today_usage"`)
	assert.Contains(t, outJSON, `"model_breakdown"`)
	assert.Contains(t, outJSON, `"agent_breakdown"`)

	// 5. Test stats alias: agyent tokens
	resetFlags(rootCmd)
	buf.Reset()
	rootCmd.SetArgs([]string{"--config", cfgPath, "tokens", "--agent", "coder"})
	err = rootCmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Filtered Agent: [coder]")
	assert.Contains(t, buf.String(), "21,200 tokens")
}
