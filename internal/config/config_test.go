package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"agyent/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	assert.NotNil(t, cfg)
	assert.Equal(t, "127.0.0.1", cfg.Server.Host)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "polling", cfg.Telegram.Mode)
	assert.Equal(t, "agy", cfg.AGY.BinaryPath)
	assert.Equal(t, 300, cfg.AGY.DefaultTimeoutSeconds)
	assert.Equal(t, "high", cfg.AGY.DefaultEffort)
	assert.Equal(t, "accept-edits", cfg.AGY.DefaultMode)
	assert.True(t, cfg.AGY.DangerouslySkipPermissions)
	assert.Equal(t, 2.0, cfg.Storage.DebounceSeconds)
	assert.Equal(t, 4.0, cfg.Storage.HeartbeatIntervalSeconds)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "text", cfg.Logging.Format)
}

func TestLoad_ValidYAML(t *testing.T) {
	yamlData := `
server:
  host: "0.0.0.0"
  port: 9090
telegram:
  bot_token: "123456:TEST_TOKEN"
  mode: "webhook"
  webhook_url: "https://example.com/webhook"
  admin_user_ids:
    - 11111
    - 22222
  allowed_group_ids:
    - "-100999"
agy:
  binary_path: "/usr/local/bin/agy"
  default_timeout_seconds: 600
  default_effort: "medium"
  default_mode: "plan"
  dangerously_skip_permissions: false
storage:
  db_path: "/var/data/agyent.db"
  agents_dir: "/var/data/agents"
  debounce_seconds: 3.5
  heartbeat_interval_seconds: 5.0
logging:
  level: "debug"
  format: "json"
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte(yamlData), 0600)
	require.NoError(t, err)

	cfg, err := config.Load(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 9090, cfg.Server.Port)
	assert.Equal(t, "123456:TEST_TOKEN", cfg.Telegram.BotToken)
	assert.Equal(t, "webhook", cfg.Telegram.Mode)
	assert.Equal(t, "https://example.com/webhook", cfg.Telegram.WebhookURL)
	assert.Equal(t, []int64{11111, 22222}, cfg.Telegram.AdminUserIDs)
	assert.Equal(t, []string{"-100999"}, cfg.Telegram.AllowedGroupIDs)
	assert.Equal(t, filepath.Clean("/usr/local/bin/agy"), cfg.AGY.BinaryPath)
	assert.Equal(t, 600, cfg.AGY.DefaultTimeoutSeconds)
	assert.Equal(t, "medium", cfg.AGY.DefaultEffort)
	assert.Equal(t, "plan", cfg.AGY.DefaultMode)
	assert.False(t, cfg.AGY.DangerouslySkipPermissions)
	assert.Equal(t, filepath.Clean("/var/data/agyent.db"), cfg.Storage.DBPath)
	assert.Equal(t, filepath.Clean("/var/data/agents"), cfg.Storage.AgentsDir)
	assert.Equal(t, 3.5, cfg.Storage.DebounceSeconds)
	assert.Equal(t, 5.0, cfg.Storage.HeartbeatIntervalSeconds)
	assert.Equal(t, "debug", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
}

func TestLoad_MalformedYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "malformed.yaml")
	err := os.WriteFile(configPath, []byte("server:\n  host: [unclosed bracket"), 0600)
	require.NoError(t, err)

	cfg, err := config.Load(configPath)
	assert.Error(t, err)
	assert.Nil(t, cfg)
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("AGYENT_SERVER_HOST", "10.0.0.1")
	t.Setenv("AGYENT_SERVER_PORT", "7777")
	t.Setenv("AGYENT_TELEGRAM_BOT_TOKEN", "ENV_BOT_TOKEN")
	t.Setenv("AGYENT_TELEGRAM_MODE", "polling")
	t.Setenv("AGYENT_TELEGRAM_ADMIN_USER_IDS", "101; 202, 303 404")
	t.Setenv("AGYENT_TELEGRAM_ALLOWED_GROUP_IDS", "-1001; -1002, -1003")
	t.Setenv("AGYENT_AGY_BINARY_PATH", "custom-agy")
	t.Setenv("AGYENT_AGY_DEFAULT_TIMEOUT_SECONDS", "120")
	t.Setenv("AGYENT_AGY_DEFAULT_EFFORT", "low")
	t.Setenv("AGYENT_AGY_DEFAULT_MODE", "plan")
	t.Setenv("AGYENT_AGY_DANGEROUSLY_SKIP_PERMISSIONS", "false")
	t.Setenv("AGYENT_STORAGE_DB_PATH", "/tmp/test.db")
	t.Setenv("AGYENT_STORAGE_AGENTS_DIR", "/tmp/agents")
	t.Setenv("AGYENT_STORAGE_DEBOUNCE_SECONDS", "1.5")
	t.Setenv("AGYENT_STORAGE_HEARTBEAT_INTERVAL_SECONDS", "3.0")
	t.Setenv("AGYENT_LOGGING_LEVEL", "warn")
	t.Setenv("AGYENT_LOGGING_FORMAT", "json")

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "10.0.0.1", cfg.Server.Host)
	assert.Equal(t, 7777, cfg.Server.Port)
	assert.Equal(t, "ENV_BOT_TOKEN", cfg.Telegram.BotToken)
	assert.Equal(t, []int64{101, 202, 303, 404}, cfg.Telegram.AdminUserIDs)
	assert.Equal(t, []string{"-1001", "-1002", "-1003"}, cfg.Telegram.AllowedGroupIDs)
	assert.Equal(t, "custom-agy", cfg.AGY.BinaryPath)
	assert.Equal(t, 120, cfg.AGY.DefaultTimeoutSeconds)
	assert.Equal(t, "low", cfg.AGY.DefaultEffort)
	assert.Equal(t, "plan", cfg.AGY.DefaultMode)
	assert.False(t, cfg.AGY.DangerouslySkipPermissions)
	assert.Equal(t, filepath.Clean("/tmp/test.db"), cfg.Storage.DBPath)
	assert.Equal(t, filepath.Clean("/tmp/agents"), cfg.Storage.AgentsDir)
	assert.Equal(t, 1.5, cfg.Storage.DebounceSeconds)
	assert.Equal(t, 3.0, cfg.Storage.HeartbeatIntervalSeconds)
	assert.Equal(t, "warn", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *config.Config)
		wantErr bool
	}{
		{
			name: "Valid Config",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = "123:token"
				c.Telegram.AdminUserIDs = []int64{123}
			},
			wantErr: false,
		},
		{
			name: "Missing Bot Token",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = ""
				c.Telegram.AdminUserIDs = []int64{123}
			},
			wantErr: true,
		},
		{
			name: "Missing Admin IDs",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = "123:token"
				c.Telegram.AdminUserIDs = []int64{}
			},
			wantErr: true,
		},
		{
			name: "Missing DB Path",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = "123:token"
				c.Telegram.AdminUserIDs = []int64{123}
				c.Storage.DBPath = ""
			},
			wantErr: true,
		},
		{
			name: "Missing Agents Dir",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = "123:token"
				c.Telegram.AdminUserIDs = []int64{123}
				c.Storage.AgentsDir = ""
			},
			wantErr: true,
		},
		{
			name: "Negative Debounce Seconds",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = "123:token"
				c.Telegram.AdminUserIDs = []int64{123}
				c.Storage.DebounceSeconds = -1.0
			},
			wantErr: true,
		},
		{
			name: "Invalid Telegram Mode",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = "123:token"
				c.Telegram.AdminUserIDs = []int64{123}
				c.Telegram.Mode = "invalid_mode"
			},
			wantErr: true,
		},
		{
			name: "Webhook Mode Missing WebhookURL",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = "123:token"
				c.Telegram.AdminUserIDs = []int64{123}
				c.Telegram.Mode = "webhook"
				c.Telegram.WebhookURL = ""
			},
			wantErr: true,
		},
		{
			name: "Webhook Mode Valid",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = "123:token"
				c.Telegram.AdminUserIDs = []int64{123}
				c.Telegram.Mode = "webhook"
				c.Telegram.WebhookURL = "https://example.com/webhook"
			},
			wantErr: false,
		},
		{
			name: "Invalid Logging Level",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = "123:token"
				c.Telegram.AdminUserIDs = []int64{123}
				c.Logging.Level = "invalid_level"
			},
			wantErr: true,
		},
		{
			name: "Invalid Logging Format",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = "123:token"
				c.Telegram.AdminUserIDs = []int64{123}
				c.Logging.Format = "invalid_format"
			},
			wantErr: true,
		},
		{
			name: "Valid Logging Level and Format",
			mutate: func(c *config.Config) {
				c.Telegram.BotToken = "123:token"
				c.Telegram.AdminUserIDs = []int64{123}
				c.Logging.Level = "DEBUG"
				c.Logging.Format = "json"
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}

	t.Run("TelegramConfig String Masking", func(t *testing.T) {
		tc := config.TelegramConfig{
			BotToken:     "123456789:ABCdefGHIjklMNOpqrsTUVwxyz",
			Mode:         "polling",
			AdminUserIDs: []int64{12345},
		}
		str := tc.String()
		assert.Contains(t, str, "123456:***")
		assert.NotContains(t, str, "ABCdefGHIjklMNOpqrsTUVwxyz")

		tcShort := config.TelegramConfig{
			BotToken: "short",
		}
		assert.Contains(t, tcShort.String(), "***")
	})

	t.Run("Config String Masking", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Telegram.BotToken = "123456789:ABCdefGHIjklMNOpqrsTUVwxyz"
		str := cfg.String()
		assert.Contains(t, str, "123456:***")
		assert.NotContains(t, str, "ABCdefGHIjklMNOpqrsTUVwxyz")

		var nilCfg *config.Config
		assert.Equal(t, "<nil>", nilCfg.String())
	})
}

func TestSaveAndReload(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "nested", "sub", "config.yaml")

	cfg := config.DefaultConfig()
	cfg.Telegram.BotToken = "SAVED_TOKEN"
	cfg.Telegram.AdminUserIDs = []int64{99999}
	cfg.Storage.DebounceSeconds = 2.5

	err := config.Save(targetPath, cfg)
	require.NoError(t, err)

	loaded, err := config.Load(targetPath)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, "SAVED_TOKEN", loaded.Telegram.BotToken)
	assert.Equal(t, []int64{99999}, loaded.Telegram.AdminUserIDs)
	assert.Equal(t, 2.5, loaded.Storage.DebounceSeconds)
}

func TestExpandPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = "~/.agyent/test.db"
	cfg.Storage.AgentsDir = "~/agents"

	err = cfg.ExpandPaths()
	require.NoError(t, err)

	expectedDB := filepath.Clean(filepath.Join(home, ".agyent", "test.db"))
	expectedAgents := filepath.Clean(filepath.Join(home, "agents"))

	assert.Equal(t, expectedDB, cfg.Storage.DBPath)
	assert.Equal(t, expectedAgents, cfg.Storage.AgentsDir)
}

func TestResolveAgentWorkspace(t *testing.T) {
	baseDir := filepath.Clean("/home/user/.agyent")

	// Default / main agent
	assert.Equal(t, filepath.Join(baseDir, "workspace"), config.ResolveAgentWorkspace(baseDir, "agyent"))
	assert.Equal(t, filepath.Join(baseDir, "workspace"), config.ResolveAgentWorkspace(baseDir, ""))

	// Custom agents
	assert.Equal(t, filepath.Join(baseDir, "workspace-coder"), config.ResolveAgentWorkspace(baseDir, "coder"))
	assert.Equal(t, filepath.Join(baseDir, "workspace-researcher"), config.ResolveAgentWorkspace(baseDir, "researcher"))
}

func TestMigrateLegacyWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	legacyDir := filepath.Join(tmpDir, "agents", "workspace")
	targetDir := filepath.Join(tmpDir, "workspace")

	require.NoError(t, os.MkdirAll(legacyDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "IDENTITY.md"), []byte("# Identity"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "SOUL.md"), []byte("# Soul"), 0644))

	// Migrate
	err := config.MigrateLegacyWorkspace(tmpDir)
	require.NoError(t, err)

	// Check migrated files exist in targetDir
	assert.FileExists(t, filepath.Join(targetDir, "IDENTITY.md"))
	assert.FileExists(t, filepath.Join(targetDir, "SOUL.md"))
}

func TestMultiBotConfig_NormalizationAndValidation(t *testing.T) {
	t.Run("Legacy Single Bot Fallback", func(t *testing.T) {
		tg := config.TelegramConfig{
			BotToken: "123456:LEGACY_TOKEN",
		}
		bots := tg.GetNormalizedBots()
		assert.Len(t, bots, 1)
		assert.Equal(t, "default", bots[0].Name)
		assert.Equal(t, "123456:LEGACY_TOKEN", bots[0].BotToken)
		assert.Empty(t, bots[0].BindAgent)
	})

	t.Run("Multi-Bot Config with Agent Binding", func(t *testing.T) {
		tg := config.TelegramConfig{
			Bots: []config.BotConfig{
				{
					Name:      "dev_bot",
					BotToken:  "11111:DEV_TOKEN",
					BindAgent: "dev_architect",
				},
				{
					Name:      "assistant_bot",
					BotToken:  "22222:ASSISTANT_TOKEN",
					BindAgent: "personal_assistant",
				},
			},
		}
		bots := tg.GetNormalizedBots()
		assert.Len(t, bots, 2)
		assert.Equal(t, "dev_bot", bots[0].Name)
		assert.Equal(t, "dev_architect", bots[0].BindAgent)
		assert.Equal(t, "assistant_bot", bots[1].Name)
		assert.Equal(t, "personal_assistant", bots[1].BindAgent)
	})

	t.Run("Validation Error when no bots configured", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Telegram.BotToken = ""
		cfg.Telegram.Bots = nil
		cfg.Telegram.AdminUserIDs = []int64{123}
		assert.Error(t, cfg.Validate())
	})

	t.Run("Per-Agent Configuration and Preset Resolution", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Security.Preset = "balanced"
		cfg.Agents = map[string]config.AgentProfileConfig{
			"coder_bot": {
				SecurityPreset: "developer",
				DefaultModel:   "gemini-2.5-pro",
			},
			"auditor_bot": {
				SecurityPreset: "strict",
				DefaultModel:   "gemini-2.5-flash",
			},
			"unrestricted_bot": {
				SecurityPreset: "unrestricted",
			},
		}

		// Specific configured agent returns its own preset
		assert.Equal(t, "developer", cfg.ResolveAgentPreset("coder_bot"))
		assert.Equal(t, "strict", cfg.ResolveAgentPreset("auditor_bot"))
		assert.Equal(t, "unrestricted", cfg.ResolveAgentPreset("unrestricted_bot"))

		// Unlisted agent falls back to global security.preset
		assert.Equal(t, "balanced", cfg.ResolveAgentPreset("unknown_bot"))

		// Invalid preset in agent config fails validation
		cfg.Agents["invalid_bot"] = config.AgentProfileConfig{
			SecurityPreset: "super_safe",
		}
		assert.Error(t, cfg.Validate())
	})
}

