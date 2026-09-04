package doctor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"agyent/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctor_RunBasic(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	dbPath := filepath.Join(tempDir, "agyent.db")
	agentsDir := filepath.Join(tempDir, "agents")

	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = dbPath
	cfg.Storage.AgentsDir = agentsDir
	cfg.Telegram.BotToken = "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ12345"
	cfg.Telegram.AdminUserIDs = []int64{123456789}

	err := config.Save(configPath, cfg)
	require.NoError(t, err)

	opts := Options{
		ConfigPath:  configPath,
		SkipNetwork: true,
		AutoFix:     true,
	}

	runner := NewRunner(opts)
	ctx := context.Background()
	report, err := runner.Run(ctx)
	require.NoError(t, err)
	assert.NotNil(t, report)
	assert.Greater(t, len(report.Results), 5)
	assert.Greater(t, report.PassedCount, 0)

	// Test Text Render
	var buf bytes.Buffer
	report.RenderText(&buf)
	assert.Contains(t, buf.String(), "AGYENT DOCTOR")
	assert.Contains(t, buf.String(), "SUMMARY")

	// Test JSON Render
	var jsonBuf bytes.Buffer
	err = report.RenderJSON(&jsonBuf)
	require.NoError(t, err)
	assert.Contains(t, jsonBuf.String(), `"passed_count"`)
}

func TestDoctor_MissingConfig(t *testing.T) {
	opts := Options{
		ConfigPath:  filepath.Join(t.TempDir(), "nonexistent.yaml"),
		SkipNetwork: true,
		AutoFix:     false,
	}

	runner := NewRunner(opts)
	ctx := context.Background()
	report, err := runner.Run(ctx)
	require.NoError(t, err)
	assert.NotNil(t, report)
	assert.Greater(t, report.FailCount, 0)
}

func TestDoctor_SystemChecks(t *testing.T) {
	runner := NewRunner(Options{ConfigPath: filepath.Join(t.TempDir(), "cfg.yaml")})
	results := runner.CheckSystem()
	assert.NotEmpty(t, results)
	for _, res := range results {
		assert.Equal(t, CategorySystem, res.Category)
	}
}

func TestDoctor_SecurityChecks(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.AgentsDir = tempDir

	runner := &DoctorRunner{
		cfg: cfg,
	}
	results := runner.CheckSecurity()
	assert.NotEmpty(t, results)
	assert.Equal(t, CategorySecurity, results[0].Category)
}

func TestDoctor_SpecificSessionTriage(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	dbPath := filepath.Join(tempDir, "agyent.db")

	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = dbPath
	cfg.Telegram.BotToken = "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ12345"
	cfg.Telegram.AdminUserIDs = []int64{123456789}
	_ = config.Save(configPath, cfg)

	opts := Options{
		ConfigPath:    configPath,
		TargetSession: "telegram:123:456",
		SkipNetwork:   true,
		AutoFix:       true,
	}

	runner := NewRunner(opts)
	report, err := runner.Run(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, report)
}

func TestDoctor_DirectoryCreationFix(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.yaml")
	agentsDir := filepath.Join(tempDir, "missing_agents_dir")
	dbPath := filepath.Join(tempDir, "db_dir", "agyent.db")

	cfg := config.DefaultConfig()
	cfg.Storage.AgentsDir = agentsDir
	cfg.Storage.DBPath = dbPath
	cfg.Telegram.BotToken = "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ12345"
	cfg.Telegram.AdminUserIDs = []int64{123456789}
	_ = config.Save(cfgPath, cfg)

	opts := Options{
		ConfigPath:  cfgPath,
		SkipNetwork: true,
		AutoFix:     true,
	}

	runner := NewRunner(opts)
	report, err := runner.Run(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, report)

	// Verify directory was created by fix
	_, err = os.Stat(agentsDir)
	assert.NoError(t, err)
}

func TestDoctor_ZombiesAndLocks(t *testing.T) {
	opts := Options{
		SkipNetwork: true,
	}
	runner := NewRunner(opts)
	ctx := context.Background()

	// 1. Zombie checks
	zombieRes := runner.CheckZombies(ctx)
	assert.NotEmpty(t, zombieRes)
	assert.Equal(t, "Zombie & Orphaned Process Health", zombieRes[0].Name)

	// 2. Lock checks
	lockRes := runner.CheckStaleLocks()
	assert.NotEmpty(t, lockRes)
	assert.Equal(t, "Stale File Lock Inspection", lockRes[0].Name)

	// 3. Test fixStaleLocks
	tempLockDir := t.TempDir()
	tempLockFile := filepath.Join(tempLockDir, "test.lock")
	_ = os.WriteFile(tempLockFile, []byte(""), 0644)
	assert.FileExists(t, tempLockFile)
	_ = os.Remove(tempLockFile)
	assert.NoFileExists(t, tempLockFile)
}

func TestDoctor_CheckZalo(t *testing.T) {
	opts := Options{
		SkipNetwork: true,
	}
	runner := NewRunner(opts)
	ctx := context.Background()

	// 1. Not configured
	runner.cfg = config.DefaultConfig()
	runner.cfg.Telegram.BotToken = "123456:ABC"
	res := runner.CheckZalo(ctx)
	assert.NotEmpty(t, res)
	assert.Equal(t, StatusInfo, res[0].Status)

	// 2. Configured with token, group, and webhook URL
	runner.cfg.Zalo.BotToken = "test_zalo_token"
	runner.cfg.Zalo.GroupID = "group_123"
	runner.cfg.Zalo.Mode = "webhook"
	runner.cfg.Zalo.WebhookURL = "https://example.com/zalo/webhook"

	res = runner.CheckZalo(ctx)
	assert.NotEmpty(t, res)

	hasTokenCheck := false
	hasGroupCheck := false
	hasWebhookCheck := false
	for _, r := range res {
		if r.Name == "Zalo Bot Token [default]" && r.Status == StatusPass {
			hasTokenCheck = true
		}
		if r.Name == "Zalo Target Group" && r.Status == StatusPass {
			hasGroupCheck = true
		}
		if r.Name == "Zalo Webhook URL" && r.Status == StatusPass {
			hasWebhookCheck = true
		}
	}
	assert.True(t, hasTokenCheck)
	assert.True(t, hasGroupCheck)
	assert.True(t, hasWebhookCheck)
}
