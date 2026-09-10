package doctor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"agyent/internal/config"
	_ "modernc.org/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected int
	}{
		{"both empty", "", "", 0},
		{"s1 empty", "", "hello", 5},
		{"s2 empty", "world", "", 5},
		{"identical", "allowed_paths", "allowed_paths", 0},
		{"case sensitive difference", "Path", "path", 1},
		{"one substitution", "alowed_paths", "allowed_paths", 1},
		{"one deletion", "security_preset", "securty_preset", 1},
		{"one insertion", "workspce", "workspace", 1},
		{"two edits", "dngrously", "dangerously", 2},
		{"completely different", "server", "telegram", 6},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := levenshtein(tc.s1, tc.s2)
			assert.Equal(t, tc.expected, res)
		})
	}
}

func TestFindClosestField(t *testing.T) {
	candidates := []string{
		"allowed_paths",
		"security_preset",
		"workspace_path",
		"dangerously_skip_permissions",
		"default_effort",
	}

	tests := []struct {
		name         string
		query        string
		expectedBest string
		expectedDist int
	}{
		{"exact match", "allowed_paths", "allowed_paths", 0},
		{"single typo", "alowed_paths", "allowed_paths", 1},
		{"case insensitive", "ALLOWED_PATHS", "allowed_paths", 0},
		{"two edits", "securty_prest", "security_preset", 2},
		{"three edits", "workspc_pth", "workspace_path", 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			best, dist := findClosestField(tc.query, candidates)
			assert.Equal(t, tc.expectedBest, best)
			assert.Equal(t, tc.expectedDist, dist)
		})
	}

	t.Run("distant query exceeds suggestion threshold", func(t *testing.T) {
		_, dist := findClosestField("unrelated_field_name", candidates)
		assert.Greater(t, dist, 3)
	})
}

func TestIsSubpath(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		base     string
		expected bool
	}{
		{"direct child", "/home/user/.ssh/id_rsa", "/home/user/.ssh", true},
		{"nested child", "/home/user/.ssh/keys/id_ed25519", "/home/user/.ssh", true},
		{"same directory", "/home/user/.ssh", "/home/user/.ssh", false},
		{"parent of base", "/home/user", "/home/user/.ssh", false},
		{"sibling directory", "/home/user/projects", "/home/user/.ssh", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := isSubpath(tc.target, tc.base)
			assert.Equal(t, tc.expected, res)
		})
	}
}

func TestIsHookConfigFile(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"relative workspace hooks", ".agents/hooks.json", true},
		{"absolute workspace hooks", "/Users/alice/repo/.agents/hooks.json", true},
		{"global gemini hooks", "~/.gemini/config/hooks.json", true},
		{"absolute gemini hooks", "/Users/alice/.gemini/config/hooks.json", true},
		{"generic hooks json", "/etc/hooks.json", true},
		{"unrelated json", "/Users/alice/config.json", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := isHookConfigFile(tc.path)
			assert.Equal(t, tc.expected, res)
		})
	}
}

func TestCheckConfiguration_StrictLinting_Typos(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	yamlContent := `
server:
  host: "127.0.0.1"
  port: 8080
telegram:
  bot_token: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
  admin_user_ids: [123456]
security:
  enabled: true
  preset: "developer"
  filesystem:
    alowed_paths:
      - "/tmp"
`
	err := os.WriteFile(configPath, []byte(yamlContent), 0600)
	require.NoError(t, err)

	runner := NewRunner(Options{ConfigPath: configPath, SkipNetwork: true})
	results, _ := runner.CheckConfiguration(context.Background())

	var foundTypoCheck bool
	for _, res := range results {
		if res.Name == "Configuration Key Linting" && res.Status == StatusFail {
			foundTypoCheck = true
			assert.Contains(t, res.Message, `Unknown configuration field "alowed_paths"`)
			assert.Contains(t, res.Message, `Did you mean "allowed_paths"?`)
			assert.Contains(t, res.Remediation, `Replace "alowed_paths" with "allowed_paths"`)
		}
	}
	assert.True(t, foundTypoCheck, "Expected Configuration Key Linting failure for 'alowed_paths'")
}

func TestCheckConfiguration_StrictLinting_ScopedDeprecation(t *testing.T) {
	tests := []struct {
		name               string
		yamlSnippet        string
		expectedCheckName  string
		expectedStatus     CheckStatus
		expectedInMessage  string
	}{
		{
			name: "valid deprecated field on matching struct",
			yamlSnippet: `
server:
  host: "127.0.0.1"
  port: 8080
telegram:
  bot_token: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
  admin_user_ids: [123456]
security:
  enabled: true
  preset: "developer"
  commands:
    whitelist_patterns:
      - "git .*"
`,
			expectedCheckName: "Deprecated Configuration Key",
			expectedStatus:    StatusWarn,
			expectedInMessage: "whitelist_patterns",
		},
		{
			name: "deprecated field name on wrong struct is a schema failure not deprecation",
			yamlSnippet: `
server:
  host: "127.0.0.1"
  port: 8080
  preset: "8080"
telegram:
  bot_token: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
  admin_user_ids: [123456]
`,
			expectedCheckName: "Configuration Key Linting",
			expectedStatus:    StatusFail,
			expectedInMessage: "preset",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			err := os.WriteFile(configPath, []byte(tc.yamlSnippet), 0600)
			require.NoError(t, err)

			runner := NewRunner(Options{ConfigPath: configPath, SkipNetwork: true})
			results, _ := runner.CheckConfiguration(context.Background())

			var matched bool
			for _, res := range results {
				if res.Name == tc.expectedCheckName && res.Status == tc.expectedStatus {
					if res.Message != "" && assert.ObjectsAreEqual(true, true) {
						matched = true
						assert.Contains(t, res.Message, tc.expectedInMessage)
					}
				}
			}
			assert.True(t, matched, "Expected to match check %q with status %s", tc.expectedCheckName, tc.expectedStatus)
		})
	}
}

func TestCheckConfiguration_InvalidRegex(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	yamlContent := `
server:
  host: "127.0.0.1"
  port: 8080
telegram:
  bot_token: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
  admin_user_ids: [123456]
security:
  enabled: true
  preset: "developer"
  commands:
    custom_blacklist:
      - "(?i)[invalid(regex"
    custom_whitelist:
      - "[another(unclosed"
`
	err := os.WriteFile(configPath, []byte(yamlContent), 0600)
	require.NoError(t, err)

	runner := NewRunner(Options{ConfigPath: configPath, SkipNetwork: true})
	results, _ := runner.CheckConfiguration(context.Background())

	var foundBlacklistRegex, foundWhitelistRegex bool
	for _, res := range results {
		if res.Name == "Command Blacklist RegEx" && res.Status == StatusFail {
			foundBlacklistRegex = true
			assert.Contains(t, res.Message, "Invalid regular expression")
		}
		if res.Name == "Command Whitelist RegEx" && res.Status == StatusFail {
			foundWhitelistRegex = true
			assert.Contains(t, res.Message, "Invalid regular expression")
		}
	}
	assert.True(t, foundBlacklistRegex, "Expected Command Blacklist RegEx failure")
	assert.True(t, foundWhitelistRegex, "Expected Command Whitelist RegEx failure")
}

func TestCheckConfiguration_ForbiddenPathCollision(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	yamlContent := `
server:
  host: "127.0.0.1"
  port: 8080
telegram:
  bot_token: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
  admin_user_ids: [123456]
security:
  enabled: true
  preset: "developer"
  filesystem:
    allowed_paths:
      - "~/.ssh"
      - "~/.ssh/id_rsa"
      - "/tmp/myrepo/.agents/hooks.json"
`
	err := os.WriteFile(configPath, []byte(yamlContent), 0600)
	require.NoError(t, err)

	runner := NewRunner(Options{ConfigPath: configPath, SkipNetwork: true})
	results, _ := runner.CheckConfiguration(context.Background())

	var foundDirectCollision, foundSubpathCollision, foundHookCollision bool
	for _, res := range results {
		if res.Name == "Forbidden Path Collision" && res.Status == StatusFail {
			foundDirectCollision = true
			assert.Contains(t, res.Message, "~/.ssh")
		}
		if res.Name == "Forbidden Subpath Collision" && res.Status == StatusFail {
			foundSubpathCollision = true
			assert.Contains(t, res.Message, "~/.ssh/id_rsa")
		}
		if res.Name == "Forbidden Hook File Collision" && res.Status == StatusFail {
			foundHookCollision = true
			assert.Contains(t, res.Message, ".agents/hooks.json")
		}
	}
	assert.True(t, foundDirectCollision, "Expected Forbidden Path Collision failure")
	assert.True(t, foundSubpathCollision, "Expected Forbidden Subpath Collision failure")
	assert.True(t, foundHookCollision, "Expected Forbidden Hook File Collision failure")
}

func TestCheckConfiguration_AgentWorkspaceCollisionsAndSubsumption(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	yamlContent := `
server:
  host: "127.0.0.1"
  port: 8080
telegram:
  bot_token: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
  admin_user_ids: [123456]
security:
  enabled: true
  preset: "developer"
  filesystem:
    allowed_paths:
      - "~/.agyent"
agents:
  unsafe_agent:
    name: "unsafe_agent"
    workspace_path: "~/.ssh"
`
	err := os.WriteFile(configPath, []byte(yamlContent), 0600)
	require.NoError(t, err)

	runner := NewRunner(Options{ConfigPath: configPath, SkipNetwork: true})
	results, _ := runner.CheckConfiguration(context.Background())

	var foundWorkspaceCollision, foundSubsumingWarn bool
	for _, res := range results {
		if res.Name == "Forbidden Path Collision" && res.Status == StatusFail {
			if res.Message != "" && filepath.Base(res.Message) != "" {
				foundWorkspaceCollision = true
			}
		}
		if res.Name == "Subsuming Allowed Path" && res.Status == StatusWarn {
			foundSubsumingWarn = true
			assert.Contains(t, res.Message, "subsumes forbidden path")
		}
	}
	assert.True(t, foundWorkspaceCollision, "Expected agent workspace collision with ~/.ssh")
	assert.True(t, foundSubsumingWarn, "Expected subsuming allowed path warning for ~/.agyent")
}

func TestCheckConfiguration_NonExistentAndRelativePaths(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	yamlContent := `
server:
  host: "127.0.0.1"
  port: 8080
telegram:
  bot_token: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
  admin_user_ids: [123456]
security:
  enabled: true
  preset: "developer"
  filesystem:
    allowed_paths:
      - "relative/workspace/folder"
      - "/nonexistent/test_dir_agyent_9999"
`
	err := os.WriteFile(configPath, []byte(yamlContent), 0600)
	require.NoError(t, err)

	runner := NewRunner(Options{ConfigPath: configPath, SkipNetwork: true})
	results, _ := runner.CheckConfiguration(context.Background())

	var foundRelativeWarn, foundMissingWarn bool
	for _, res := range results {
		if res.Name == "Relative Allowed Path" && res.Status == StatusWarn {
			foundRelativeWarn = true
			assert.Contains(t, res.Message, "relative")
		}
		if res.Name == "Missing Allowed Directory" && res.Status == StatusWarn {
			foundMissingWarn = true
			assert.Contains(t, res.Message, "does not exist on disk")
		}
	}
	assert.True(t, foundRelativeWarn, "Expected Relative Allowed Path warning")
	assert.True(t, foundMissingWarn, "Expected Missing Allowed Directory warning")
}

func TestCheckConfiguration_EnumValidation(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	yamlContent := `
server:
  host: "127.0.0.1"
  port: 8080
telegram:
  bot_token: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
  admin_user_ids: [123456]
agy:
  default_effort: "super_fast"
  default_mode: "auto_pilot"
  queue_mode: "priority"
  append_strategy: "overwrite"
security:
  enabled: true
  preset: "nonexistent_preset"
  dlp:
    redaction_mode: "super_secret"
agents:
  bad_agent:
    name: "bad_agent"
    security_preset: "magic"
    default_effort: "ultra"
`
	err := os.WriteFile(configPath, []byte(yamlContent), 0600)
	require.NoError(t, err)

	runner := NewRunner(Options{ConfigPath: configPath, SkipNetwork: true})
	results, _ := runner.CheckConfiguration(context.Background())

	var foundEffortFail, foundModeFail, foundQueueFail, foundAppendFail, foundSecPresetFail, foundDLPFail, foundAgentPresetFail, foundAgentEffortFail bool
	for _, res := range results {
		if res.Name == "AGY Default Effort Enum" && res.Status == StatusFail {
			foundEffortFail = true
		}
		if res.Name == "AGY Default Mode Enum" && res.Status == StatusFail {
			foundModeFail = true
		}
		if res.Name == "AGY Queue Mode Enum" && res.Status == StatusFail {
			foundQueueFail = true
		}
		if res.Name == "AGY Append Strategy Enum" && res.Status == StatusFail {
			foundAppendFail = true
		}
		if res.Name == "Security Preset Enum" && res.Status == StatusFail {
			foundSecPresetFail = true
		}
		if res.Name == "DLP Redaction Mode Enum" && res.Status == StatusFail {
			foundDLPFail = true
		}
		if res.Name == "Agent bad_agent Security Preset Enum" && res.Status == StatusFail {
			foundAgentPresetFail = true
		}
		if res.Name == "Agent bad_agent Default Effort Enum" && res.Status == StatusFail {
			foundAgentEffortFail = true
		}
	}
	assert.True(t, foundEffortFail, "Expected AGY Default Effort Enum fail")
	assert.True(t, foundModeFail, "Expected AGY Default Mode Enum fail")
	assert.True(t, foundQueueFail, "Expected AGY Queue Mode Enum fail")
	assert.True(t, foundAppendFail, "Expected AGY Append Strategy Enum fail")
	assert.True(t, foundSecPresetFail, "Expected Security Preset Enum fail")
	assert.True(t, foundDLPFail, "Expected DLP Redaction Mode Enum fail")
	assert.True(t, foundAgentPresetFail, "Expected Agent bad_agent Security Preset Enum fail")
	assert.True(t, foundAgentEffortFail, "Expected Agent bad_agent Default Effort Enum fail")
}

func TestCheckConfiguration_SecurityPostureWarnings(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	yamlContent := `
server:
  host: "127.0.0.1"
  port: 8080
telegram:
  bot_token: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
  admin_user_ids: [123456]
agy:
  dangerously_skip_permissions: true
security:
  enabled: false
  preset: "full_access"
agents:
  admin_agent:
    name: "admin_agent"
    security_preset: "unrestricted"
`
	err := os.WriteFile(configPath, []byte(yamlContent), 0600)
	require.NoError(t, err)

	runner := NewRunner(Options{ConfigPath: configPath, SkipNetwork: true})
	results, _ := runner.CheckConfiguration(context.Background())

	var foundBypassWarn, foundDisabledWarn, foundGlobalPresetWarn, foundAgentPresetWarn bool
	for _, res := range results {
		if res.Name == "AGY Permission Bypass Warning" && res.Status == StatusWarn {
			foundBypassWarn = true
		}
		if res.Name == "Security Gateway Disabled" && res.Status == StatusWarn {
			foundDisabledWarn = true
		}
		if res.Name == "Unrestricted Security Preset" && res.Status == StatusWarn {
			foundGlobalPresetWarn = true
		}
		if res.Name == "Agent admin_agent Unrestricted Preset" && res.Status == StatusWarn {
			foundAgentPresetWarn = true
		}
	}
	assert.True(t, foundBypassWarn, "Expected AGY Permission Bypass Warning")
	assert.True(t, foundDisabledWarn, "Expected Security Gateway Disabled Warning")
	assert.True(t, foundGlobalPresetWarn, "Expected Unrestricted Security Preset Warning for full_access")
	assert.True(t, foundAgentPresetWarn, "Expected Agent Unrestricted Preset Warning")
}

func TestCheckConfiguration_ChannelTokenRedundancy(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	yamlContent := `
server:
  host: "127.0.0.1"
  port: 8080
telegram:
  bot_token: "123456:LEGACY_TOP_LEVEL_TOKEN"
  bots:
    - name: "bot1"
      bot_token: "123456:MULTI_BOT_TOKEN_1"
  admin_user_ids: [123456]
zalo:
  bot_token: "ZALO_LEGACY_TOKEN"
  bots:
    - name: "zalo_bot_1"
      bot_token: "ZALO_MULTI_TOKEN_1"
`
	err := os.WriteFile(configPath, []byte(yamlContent), 0600)
	require.NoError(t, err)

	runner := NewRunner(Options{ConfigPath: configPath, SkipNetwork: true})
	results, _ := runner.CheckConfiguration(context.Background())

	var foundTgRedundancy, foundZaloRedundancy bool
	for _, res := range results {
		if res.Name == "Telegram Token Redundancy" && res.Status == StatusWarn {
			foundTgRedundancy = true
		}
		if res.Name == "Zalo Token Redundancy" && res.Status == StatusWarn {
			foundZaloRedundancy = true
		}
	}
	assert.True(t, foundTgRedundancy, "Expected Telegram Token Redundancy warning")
	assert.True(t, foundZaloRedundancy, "Expected Zalo Token Redundancy warning")
}

func TestCheckConfiguration_AgentDrift(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "agyent.db")
	configPath := filepath.Join(tempDir, "config.yaml")

	// Canonical SQLite schema for agents (000001_init_schema.up.sql & 000008_agent_security_preset.up.sql)
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	_, err = db.Exec(`
		CREATE TABLE agents (
			name TEXT PRIMARY KEY,
			security_preset TEXT NOT NULL
		);
		INSERT INTO agents (name, security_preset) VALUES ('coder', 'developer');
	`)
	require.NoError(t, err)
	db.Close()

	yamlContent := `
server:
  host: "127.0.0.1"
  port: 8080
storage:
  db_path: ` + dbPath + `
telegram:
  bot_token: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
  admin_user_ids: [123456]
agents:
  coder:
    name: "coder"
    security_preset: "strict"
  reviewer:
    name: "reviewer"
    security_preset: "read_only"
`
	err = os.WriteFile(configPath, []byte(yamlContent), 0600)
	require.NoError(t, err)

	runner := NewRunner(Options{ConfigPath: configPath, SkipNetwork: true})
	results, _ := runner.CheckConfiguration(context.Background())

	var foundPresetDrift, foundUnregisteredAgent bool
	for _, res := range results {
		if res.Name == "Agent Preset Drift: coder" && res.Status == StatusWarn {
			foundPresetDrift = true
			assert.Contains(t, res.Message, `config.yaml specifies "strict", but database records "developer"`)
		}
		if res.Name == "Agent Registration: reviewer" && res.Status == StatusInfo {
			foundUnregisteredAgent = true
			assert.Contains(t, res.Message, "not yet provisioned in SQLite")
		}
	}
	assert.True(t, foundPresetDrift, "Expected Agent Preset Drift warning for coder")
	assert.True(t, foundUnregisteredAgent, "Expected Agent Registration info for reviewer")
}

func TestCheckConfiguration_ValidCleanConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = filepath.Join(tempDir, "agyent.db")
	cfg.Storage.AgentsDir = filepath.Join(tempDir, "agents")
	cfg.Telegram.BotToken = "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ12345"
	cfg.Telegram.AdminUserIDs = []int64{123456789}

	err := config.Save(configPath, cfg)
	require.NoError(t, err)

	runner := NewRunner(Options{ConfigPath: configPath, SkipNetwork: true})
	results, loadedCfg := runner.CheckConfiguration(context.Background())
	assert.NotNil(t, loadedCfg)

	for _, res := range results {
		assert.NotEqual(t, StatusFail, res.Status, "Unexpected FAIL on valid clean config: %s (%s)", res.Name, res.Message)
	}

	var foundSchemaPass bool
	for _, res := range results {
		if res.Name == "Configuration Schema & Field Linting" && res.Status == StatusPass {
			foundSchemaPass = true
		}
	}
	assert.True(t, foundSchemaPass, "Expected Configuration Schema & Field Linting PASS")
}
