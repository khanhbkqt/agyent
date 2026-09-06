package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agyent/internal/core/domain"

	"gopkg.in/yaml.v3"
)

// ServerConfig contains HTTP server configuration.
type ServerConfig struct {
	Host string `yaml:"host" json:"host"`
	Port int    `yaml:"port" json:"port"`
}

// BotConfig represents an individual bot instance configuration in a multi-bot gateway setup.
type BotConfig struct {
	Name      string `yaml:"name" json:"name"`
	BotToken  string `yaml:"bot_token" json:"bot_token"`
	BindAgent string `yaml:"bind_agent" json:"bind_agent"` // Optional: Dedicated agent persona binding (e.g. "dev_architect")
}

// TelegramConfig contains Telegram bot and access control configuration.
type TelegramConfig struct {
	BotToken        string      `yaml:"bot_token" json:"bot_token"` // Legacy single-bot token fallback
	Bots            []BotConfig `yaml:"bots" json:"bots"`           // Multi-bot lifecycle pool
	Mode            string      `yaml:"mode" json:"mode"`           // "polling" or "webhook"
	WebhookURL      string      `yaml:"webhook_url" json:"webhook_url"`
	SecretToken     string      `yaml:"secret_token" json:"secret_token"` // Secret token for X-Telegram-Bot-Api-Secret-Token
	AdminUserIDs    []int64     `yaml:"admin_user_ids" json:"admin_user_ids"`
	AllowedGroupIDs []string    `yaml:"allowed_group_ids" json:"allowed_group_ids"`
}

// GetNormalizedBots returns the full list of bot configurations.
// If Bots is empty but BotToken is set, it synthesizes a single BotConfig from BotToken.
func (t TelegramConfig) GetNormalizedBots() []BotConfig {
	if len(t.Bots) > 0 {
		return t.Bots
	}
	if strings.TrimSpace(t.BotToken) != "" {
		return []BotConfig{
			{
				Name:     "default",
				BotToken: strings.TrimSpace(t.BotToken),
			},
		}
	}
	return nil
}

// ZaloConfig contains Zalo bot platform and access control configuration.
type ZaloConfig struct {
	BotToken        string      `yaml:"bot_token" json:"bot_token"` // Bot Token from Zalo Bot Platform
	Bots            []BotConfig `yaml:"bots" json:"bots"`           // Multi-bot lifecycle pool
	GroupID         string      `yaml:"group_id" json:"group_id"`   // Target Group Chat ID
	AdminUserIDs    []string    `yaml:"admin_user_ids" json:"admin_user_ids"`
	AllowedGroupIDs []string    `yaml:"allowed_group_ids" json:"allowed_group_ids"`
	APIURL          string      `yaml:"api_url" json:"api_url"` // Defaults to https://bot-api.zaloplatforms.com if empty
	Mode            string      `yaml:"mode" json:"mode"`       // "polling" or "webhook"
	WebhookURL      string      `yaml:"webhook_url" json:"webhook_url"`
	SecretToken     string      `yaml:"secret_token" json:"secret_token"` // Webhook header verification
}

// GetNormalizedBots returns the full list of bot configurations.
// If Bots is empty but BotToken is set, it synthesizes a single BotConfig from BotToken.
func (z ZaloConfig) GetNormalizedBots() []BotConfig {
	if len(z.Bots) > 0 {
		return z.Bots
	}
	if strings.TrimSpace(z.BotToken) != "" {
		return []BotConfig{
			{
				Name:     "default",
				BotToken: strings.TrimSpace(z.BotToken),
			},
		}
	}
	return nil
}

// AGYConfig contains Antigravity CLI execution parameters.
type AGYConfig struct {
	BinaryPath                       string            `yaml:"binary_path" json:"binary_path"`
	DefaultTimeoutSeconds            int               `yaml:"default_timeout_seconds" json:"default_timeout_seconds"`
	DefaultModel                     string            `yaml:"default_model" json:"default_model"`
	DefaultEffort                    string            `yaml:"default_effort" json:"default_effort"` // "low" | "medium" | "high" | "none"
	DefaultMode                      string            `yaml:"default_mode" json:"default_mode"`     // "accept-edits" | "plan"
	ModelAliases                     map[string]string `yaml:"model_aliases" json:"model_aliases"`
	DangerouslySkipPermissions       bool              `yaml:"dangerously_skip_permissions" json:"dangerously_skip_permissions"`
	StreamingEnabled                 bool              `yaml:"streaming_enabled" json:"streaming_enabled"`
	StreamingThrottleIntervalSeconds float64           `yaml:"streaming_throttle_interval_seconds" json:"streaming_throttle_interval_seconds"`
	AutoCompact                      bool              `yaml:"auto_compact" json:"auto_compact"`
	CompactThresholdRatio            float64           `yaml:"compact_threshold_ratio" json:"compact_threshold_ratio"`
	QueueMode                        string            `yaml:"queue_mode" json:"queue_mode"`           // "fifo" | "append"
	AppendStrategy                   string            `yaml:"append_strategy" json:"append_strategy"` // "coalesce" | "replace"
	GraceTimeoutSeconds              float64           `yaml:"grace_timeout_seconds" json:"grace_timeout_seconds"`
}

// StorageConfig contains SQLite and filesystem workspace storage configuration.
type StorageConfig struct {
	DBPath                   string  `yaml:"db_path" json:"db_path"`
	AgentsDir                string  `yaml:"agents_dir" json:"agents_dir"`
	DebounceSeconds          float64 `yaml:"debounce_seconds" json:"debounce_seconds"`
	HeartbeatIntervalSeconds float64 `yaml:"heartbeat_interval_seconds" json:"heartbeat_interval_seconds"`
}

// SchedulerConfig contains configuration for scheduled tasks and heartbeats.
type SchedulerConfig struct {
	Enabled                   bool `yaml:"enabled" json:"enabled"`
	PollIntervalSeconds       int  `yaml:"poll_interval_seconds" json:"poll_interval_seconds"`
	DefaultTaskTimeoutSeconds int  `yaml:"default_task_timeout_seconds" json:"default_task_timeout_seconds"`
	HeartbeatTimeoutSeconds   int  `yaml:"heartbeat_timeout_seconds" json:"heartbeat_timeout_seconds"`
}

// LoggingConfig contains structured logging configuration.
type LoggingConfig struct {
	Level  string `yaml:"level" json:"level"`   // "debug" | "info" | "warn" | "error"
	Format string `yaml:"format" json:"format"` // "text" | "json"
}

// EvolutionConfig contains configuration for Agent Self-Learning & Evolution.
type EvolutionConfig struct {
	Enabled                  bool    `yaml:"enabled" json:"enabled"`
	IdleTimeoutMinutes       int     `yaml:"idle_timeout_minutes" json:"idle_timeout_minutes"`
	ScanIntervalMinutes      int     `yaml:"scan_interval_minutes" json:"scan_interval_minutes"`
	ConfidenceThreshold      float64 `yaml:"confidence_threshold" json:"confidence_threshold"`
	ReflectionTimeoutSeconds int     `yaml:"reflection_timeout_seconds" json:"reflection_timeout_seconds"`
	CompactionLineLimit      int     `yaml:"compaction_line_limit" json:"compaction_line_limit"`
	QueueCapacity            int     `yaml:"queue_capacity" json:"queue_capacity"`
}

// SubagentConfig contains configuration for Sub-Agent Background Dispatching & Worker Pool.
type SubagentConfig struct {
	MaxConcurrentWorkers  int    `yaml:"max_concurrent_workers" json:"max_concurrent_workers"`
	DefaultTimeoutSeconds int    `yaml:"default_timeout_seconds" json:"default_timeout_seconds"`
	DefaultModel          string `yaml:"default_model" json:"default_model"`
	DefaultEffort         string `yaml:"default_effort" json:"default_effort"`
}

// RolePolicyConfig specifies allowed and disallowed tools for a subagent role.
type RolePolicyConfig struct {
	AllowedTools    []string `yaml:"allowed_tools" json:"allowed_tools"`
	DisallowedTools []string `yaml:"disallowed_tools" json:"disallowed_tools"`
}

// AgentConfigManagementConfig controls delegated configuration editing by agents.
type AgentConfigManagementConfig struct {
	Enabled         bool     `yaml:"enabled" json:"enabled"`
	RequireApproval bool     `yaml:"require_approval" json:"require_approval"`
	ManageableFiles []string `yaml:"manageable_files" json:"manageable_files"`
}

// CommandGuardrailConfig controls shell command filtering and policies.
type CommandGuardrailConfig struct {
	Enabled         bool     `yaml:"enabled" json:"enabled"`
	CustomBlacklist []string `yaml:"custom_blacklist" json:"custom_blacklist"`
	CustomWhitelist []string `yaml:"custom_whitelist" json:"custom_whitelist"`
}

// FilesystemGuardrailConfig defines workspace jailing and forbidden paths.
type FilesystemGuardrailConfig struct {
	EnforceWorkspaceJail bool     `yaml:"enforce_workspace_jail" json:"enforce_workspace_jail"`
	AllowedPaths         []string `yaml:"allowed_paths" json:"allowed_paths"`
	ForbiddenPaths       []string `yaml:"forbidden_paths" json:"forbidden_paths"`
}

// SubagentGuardrailConfig governs sub-agent hierarchy, roles, and quotas.
type SubagentGuardrailConfig struct {
	MaxConcurrentWorkers int                         `yaml:"max_concurrent_workers" json:"max_concurrent_workers"`
	MaxCascadeDepth      int                         `yaml:"max_cascade_depth" json:"max_cascade_depth"`
	Roles                map[string]RolePolicyConfig `yaml:"roles" json:"roles"`
}

// NetworkGuardrailConfig prevents SSRF and private network exfiltration.
type NetworkGuardrailConfig struct {
	BlockCloudMetadata   bool `yaml:"block_cloud_metadata" json:"block_cloud_metadata"`
	BlockPrivateNetworks bool `yaml:"block_private_networks" json:"block_private_networks"`
	PreventDNSRebinding  bool `yaml:"prevent_dns_rebinding" json:"prevent_dns_rebinding"`
}

// DLPConfig controls secret redaction across tool outputs and chat streaming.
type DLPConfig struct {
	Enabled             bool     `yaml:"enabled" json:"enabled"`
	RedactionMode       string   `yaml:"redaction_mode" json:"redaction_mode"` // "strict" | "permissive" | "audit_only"
	SlidingWindowBytes  int      `yaml:"sliding_window_bytes" json:"sliding_window_bytes"`
	SanitizeToolOutputs bool     `yaml:"sanitize_tool_outputs" json:"sanitize_tool_outputs"`
	WhitelistedEnvKeys  []string `yaml:"whitelisted_env_keys" json:"whitelisted_env_keys"`
}

// SecurityConfig contains the Universal AI Security Gateway & Guardrails parameters.
type SecurityConfig struct {
	Enabled                      bool                        `yaml:"enabled" json:"enabled"`
	Preset                       string                      `yaml:"preset" json:"preset"` // "developer" | "balanced" | "strict" | "read_only"
	Mode                         string                      `yaml:"mode" json:"mode"`     // "interactive" | "strict"
	ApprovalTimeoutSeconds       int                         `yaml:"approval_timeout_seconds" json:"approval_timeout_seconds"`
	AdminUserIDs                 []int64                     `yaml:"admin_user_ids" json:"admin_user_ids"`
	AllowUnauthenticatedLocalDev bool                        `yaml:"allow_unauthenticated_local_dev" json:"allow_unauthenticated_local_dev"`
	AllowedProjectRoots          []string                    `yaml:"allowed_project_roots" json:"allowed_project_roots"`
	AgentConfigManagement        AgentConfigManagementConfig `yaml:"agent_config_management" json:"agent_config_management"`
	Commands                     CommandGuardrailConfig      `yaml:"commands" json:"commands"`
	Filesystem                   FilesystemGuardrailConfig   `yaml:"filesystem" json:"filesystem"`
	Subagents                    SubagentGuardrailConfig     `yaml:"subagents" json:"subagents"`
	Network                      NetworkGuardrailConfig      `yaml:"network" json:"network"`
	DLP                          DLPConfig                   `yaml:"dlp" json:"dlp"`
}

// AgentProfileConfig defines per-agent declarative configuration overrides in config.yaml.
type AgentProfileConfig struct {
	Name           string `yaml:"name" json:"name"`
	SecurityPreset string `yaml:"security_preset" json:"security_preset"` // e.g. "unrestricted", "developer", "balanced", "strict", "read_only"
	DefaultModel   string `yaml:"default_model" json:"default_model"`
	DefaultEffort  string `yaml:"default_effort" json:"default_effort"`
	WorkspacePath  string `yaml:"workspace_path" json:"workspace_path"`
	Description    string `yaml:"description" json:"description"`
	IsPublic       bool   `yaml:"is_public" json:"is_public"`
}

// RecoveryConfig controls turn auto-recovery and crash resilience.
type RecoveryConfig struct {
	Enabled                 bool   `yaml:"enabled" json:"enabled"`
	Mode                    string `yaml:"mode" json:"mode"` // "auto" | "notify_only" | "disabled"
	MaxRetries              int    `yaml:"max_retries" json:"max_retries"`
	MaxConcurrentRecoveries int    `yaml:"max_concurrent_recoveries" json:"max_concurrent_recoveries"`
	RetentionDays           int    `yaml:"retention_days" json:"retention_days"`
	SubagentsAutoResume     bool   `yaml:"subagents_auto_resume" json:"subagents_auto_resume"`
}

// Config represents the complete runtime configuration of agyent.
type Config struct {
	Server    ServerConfig                  `yaml:"server" json:"server"`
	Telegram  TelegramConfig                `yaml:"telegram" json:"telegram"`
	Zalo      ZaloConfig                    `yaml:"zalo" json:"zalo"`
	AGY       AGYConfig                     `yaml:"agy" json:"agy"`
	Storage   StorageConfig                 `yaml:"storage" json:"storage"`
	Scheduler SchedulerConfig               `yaml:"scheduler" json:"scheduler"`
	Logging   LoggingConfig                 `yaml:"logging" json:"logging"`
	Evolution EvolutionConfig               `yaml:"evolution" json:"evolution"`
	Subagent  SubagentConfig                `yaml:"subagent" json:"subagent"`
	Security  SecurityConfig                `yaml:"security" json:"security"`
	Recovery  RecoveryConfig                `yaml:"recovery" json:"recovery"`
	Agents    map[string]AgentProfileConfig `yaml:"agents" json:"agents"`
}

// DefaultConfig returns a new Config populated with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Recovery: RecoveryConfig{
			Enabled:                 true,
			Mode:                    "auto",
			MaxRetries:              1,
			MaxConcurrentRecoveries: 2,
			RetentionDays:           7,
			SubagentsAutoResume:     true,
		},
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 8080,
		},
		Telegram: TelegramConfig{
			BotToken:        "",
			Mode:            "polling",
			WebhookURL:      "",
			SecretToken:     "",
			AdminUserIDs:    []int64{},
			AllowedGroupIDs: []string{},
		},
		Zalo: ZaloConfig{
			BotToken:        "",
			GroupID:         "",
			AdminUserIDs:    []string{},
			AllowedGroupIDs: []string{},
			APIURL:          "https://bot-api.zaloplatforms.com",
			Mode:            "polling",
			WebhookURL:      "",
			SecretToken:     "",
		},
		AGY: AGYConfig{
			BinaryPath:                       "agy",
			DefaultTimeoutSeconds:            1800,
			DefaultModel:                     "",
			DefaultEffort:                    "high",
			DefaultMode:                      "accept-edits",
			ModelAliases:                     make(map[string]string),
			DangerouslySkipPermissions:       false,
			StreamingEnabled:                 true,
			StreamingThrottleIntervalSeconds: 1.5,
			AutoCompact:                      true,
			CompactThresholdRatio:            0.70,
			QueueMode:                        "fifo",
			AppendStrategy:                   "coalesce",
			GraceTimeoutSeconds:              3.0,
		},
		Storage: StorageConfig{
			DBPath:                   "~/.agyent/agyent.db",
			AgentsDir:                "~/.agyent",
			DebounceSeconds:          2.0,
			HeartbeatIntervalSeconds: 4.0,
		},
		Scheduler: SchedulerConfig{
			Enabled:                   true,
			PollIntervalSeconds:       1,
			DefaultTaskTimeoutSeconds: 300,
			HeartbeatTimeoutSeconds:   120,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
		Evolution: EvolutionConfig{
			Enabled:                  true,
			IdleTimeoutMinutes:       15,
			ScanIntervalMinutes:      5,
			ConfidenceThreshold:      0.85,
			ReflectionTimeoutSeconds: 30,
			CompactionLineLimit:      200,
			QueueCapacity:            100,
		},
		Subagent: SubagentConfig{
			MaxConcurrentWorkers:  3,
			DefaultTimeoutSeconds: 1800,
			DefaultModel:          "flash",
			DefaultEffort:         "low",
		},
		Security: GetEffectiveSecurityPreset("balanced"),
		Agents:   make(map[string]AgentProfileConfig),
	}
}

// ResolveAgentPreset returns the configured security preset for the specific agent,
// falling back to the global security.preset or "balanced".
func (c *Config) ResolveAgentPreset(agentName string) string {
	if c == nil {
		return "balanced"
	}
	if agentName != "" && len(c.Agents) > 0 {
		if a, ok := c.Agents[agentName]; ok && strings.TrimSpace(a.SecurityPreset) != "" {
			return strings.TrimSpace(a.SecurityPreset)
		}
	}
	if strings.TrimSpace(c.Security.Preset) != "" {
		return strings.TrimSpace(c.Security.Preset)
	}
	return "balanced"
}

// GetEffectiveSecurityPreset returns the default SecurityConfig corresponding to the given preset name.
func GetEffectiveSecurityPreset(preset string) SecurityConfig {
	switch strings.ToLower(preset) {
	case "unrestricted", "full_access":
		return SecurityConfig{
			Enabled:                true,
			Preset:                 "unrestricted",
			Mode:                   "autonomous",
			ApprovalTimeoutSeconds: 60,
			AgentConfigManagement: AgentConfigManagementConfig{
				Enabled:         true,
				RequireApproval: false,
				ManageableFiles: []string{"*"},
			},
			Commands: CommandGuardrailConfig{
				Enabled:         true,
				CustomBlacklist: nil,
				CustomWhitelist: []string{"*"},
			},
			Filesystem: FilesystemGuardrailConfig{
				EnforceWorkspaceJail: false,
				AllowedPaths:         []string{"*"},
				ForbiddenPaths:       nil,
			},
			Subagents: SubagentGuardrailConfig{
				MaxConcurrentWorkers: 10,
				MaxCascadeDepth:      3,
			},
			Network: NetworkGuardrailConfig{
				BlockCloudMetadata:   false,
				BlockPrivateNetworks: false,
				PreventDNSRebinding:  false,
			},
			DLP: DLPConfig{
				Enabled:             false,
				RedactionMode:       "audit_only",
				SlidingWindowBytes:  64,
				SanitizeToolOutputs: false,
			},
		}

	case "developer":
		return SecurityConfig{
			Enabled:                true,
			Preset:                 "developer",
			Mode:                   "interactive",
			ApprovalTimeoutSeconds: 60,
			AgentConfigManagement: AgentConfigManagementConfig{
				Enabled:         true,
				RequireApproval: false,
				ManageableFiles: []string{".env", ".env.*", "~/.agyent/config.yaml", "docker-compose.yml", "Makefile"},
			},
			Commands: CommandGuardrailConfig{
				Enabled:         true,
				CustomBlacklist: []string{`(?i)rm\s+-rf\s+/(boot|sys|etc)?$`, `(?i)mkfs`, `(?i)format\s+[a-z]:`},
			},
			Filesystem: FilesystemGuardrailConfig{
				EnforceWorkspaceJail: false,
				AllowedPaths:         []string{"~", "."},
				ForbiddenPaths:       []string{"~/.ssh", "~/.aws", "~/.agyent/agyent.db", ".agents/hooks.json", "~/.gemini/config/hooks.json"},
			},
			Subagents: SubagentGuardrailConfig{
				MaxConcurrentWorkers: 5,
				MaxCascadeDepth:      1,
			},
			Network: NetworkGuardrailConfig{
				BlockCloudMetadata:   true,
				BlockPrivateNetworks: false,
				PreventDNSRebinding:  true,
			},
			DLP: DLPConfig{
				Enabled:             true,
				RedactionMode:       "permissive",
				SlidingWindowBytes:  64,
				SanitizeToolOutputs: true,
				WhitelistedEnvKeys:  []string{"PORT", "HOST", "NODE_ENV", "APP_NAME", "DATABASE_URL", "API_BASE_URL"},
			},
		}

	case "strict":
		return SecurityConfig{
			Enabled:                true,
			Preset:                 "strict",
			Mode:                   "strict",
			ApprovalTimeoutSeconds: 30,
			AgentConfigManagement: AgentConfigManagementConfig{
				Enabled:         false,
				RequireApproval: true,
			},
			Commands: CommandGuardrailConfig{
				Enabled:         true,
				CustomWhitelist: []string{"go test ./...", "npm test", "git status", "git diff"},
			},
			Filesystem: FilesystemGuardrailConfig{
				EnforceWorkspaceJail: true,
				AllowedPaths:         []string{"."},
				ForbiddenPaths:       []string{"~/.ssh", "~/.aws", "~/.gnupg", "~/.kube", "~/.agyent/config.yaml", "~/.agyent/agyent.db", ".agents/hooks.json", "~/.gemini/config/hooks.json"},
			},
			Subagents: SubagentGuardrailConfig{
				MaxConcurrentWorkers: 1,
				MaxCascadeDepth:      1,
			},
			Network: NetworkGuardrailConfig{
				BlockCloudMetadata:   true,
				BlockPrivateNetworks: true,
				PreventDNSRebinding:  true,
			},
			DLP: DLPConfig{
				Enabled:             true,
				RedactionMode:       "strict",
				SlidingWindowBytes:  64,
				SanitizeToolOutputs: true,
				WhitelistedEnvKeys:  []string{"PORT", "NODE_ENV"},
			},
		}

	case "read_only":
		return SecurityConfig{
			Enabled:                true,
			Preset:                 "read_only",
			Mode:                   "strict",
			ApprovalTimeoutSeconds: 30,
			AgentConfigManagement: AgentConfigManagementConfig{
				Enabled:         false,
				RequireApproval: true,
			},
			Commands: CommandGuardrailConfig{
				Enabled: false,
			},
			Filesystem: FilesystemGuardrailConfig{
				EnforceWorkspaceJail: true,
				AllowedPaths:         []string{"."},
				ForbiddenPaths:       []string{"~/.ssh", "~/.aws", "~/.gnupg", "~/.kube", "~/.agyent/config.yaml", "~/.agyent/agyent.db", ".agents/hooks.json", "~/.gemini/config/hooks.json"},
			},
			Subagents: SubagentGuardrailConfig{
				MaxConcurrentWorkers: 2,
				MaxCascadeDepth:      1,
				Roles: map[string]RolePolicyConfig{
					"researcher": {
						AllowedTools:    []string{"view_file", "grep_search", "find_by_name", "search_web"},
						DisallowedTools: []string{"run_command", "write_to_file", "replace_file_content", "invoke_subagent", "define_subagent"},
					},
				},
			},
			Network: NetworkGuardrailConfig{
				BlockCloudMetadata:   true,
				BlockPrivateNetworks: true,
				PreventDNSRebinding:  true,
			},
			DLP: DLPConfig{
				Enabled:             true,
				RedactionMode:       "strict",
				SlidingWindowBytes:  64,
				SanitizeToolOutputs: true,
			},
		}

	case "balanced":
		fallthrough
	default:
		return SecurityConfig{
			Enabled:                true,
			Preset:                 "balanced",
			Mode:                   "interactive",
			ApprovalTimeoutSeconds: 60,
			AgentConfigManagement: AgentConfigManagementConfig{
				Enabled:         true,
				RequireApproval: true,
				ManageableFiles: []string{".env", ".env.*", "~/.agyent/config.yaml", "docker-compose.yml"},
			},
			Commands: CommandGuardrailConfig{
				Enabled:         true,
				CustomBlacklist: []string{`(?i)rm\s+-rf\s+/`, `(?i)mkfs`, `(?i)git\s+push\s+.*--force.*(main|master)`},
				CustomWhitelist: []string{"go test ./...", "npm test", "git status"},
			},
			Filesystem: FilesystemGuardrailConfig{
				EnforceWorkspaceJail: true,
				AllowedPaths:         []string{"."},
				ForbiddenPaths:       []string{"~/.ssh", "~/.aws", "~/.gnupg", "~/.kube", "~/.agyent/agyent.db", ".agents/hooks.json", "~/.gemini/config/hooks.json"},
			},
			Subagents: SubagentGuardrailConfig{
				MaxConcurrentWorkers: 3,
				MaxCascadeDepth:      1,
				Roles: map[string]RolePolicyConfig{
					"researcher": {
						AllowedTools:    []string{"view_file", "grep_search", "find_by_name", "search_web"},
						DisallowedTools: []string{"run_command", "write_to_file", "replace_file_content", "invoke_subagent", "define_subagent"},
					},
					"coder": {
						AllowedTools:    []string{"*"},
						DisallowedTools: []string{"define_subagent"},
					},
					"reviewer": {
						AllowedTools:    []string{"view_file", "grep_search", "find_by_name"},
						DisallowedTools: []string{"write_to_file", "replace_file_content", "invoke_subagent", "define_subagent"},
					},
				},
			},
			Network: NetworkGuardrailConfig{
				BlockCloudMetadata:   true,
				BlockPrivateNetworks: true,
				PreventDNSRebinding:  true,
			},
			DLP: DLPConfig{
				Enabled:             true,
				RedactionMode:       "strict",
				SlidingWindowBytes:  64,
				SanitizeToolOutputs: true,
				WhitelistedEnvKeys:  []string{"PORT", "HOST", "NODE_ENV", "APP_NAME", "DATABASE_URL"},
			},
		}
	}
}

// ResolveAgentWorkspace computes the workspace directory for an agent:
// - Main/default agent ("agyent" or empty): ~/.agyent/workspace
// - Other custom agents ("<name>"): ~/.agyent/workspace-<name>
func ResolveAgentWorkspace(agentsDir, agentName string) string {
	if agentName == "" || agentName == "agyent" {
		return filepath.Join(agentsDir, "workspace")
	}
	return filepath.Join(agentsDir, fmt.Sprintf("workspace-%s", agentName))
}

// MigrateLegacyWorkspace checks if persona files exist in a legacy workspace directory
// (e.g. ~/.agyent/agents/workspace) and seamlessly copies them to the active workspace
// (~/.agyent/workspace) if the target workspace lacks core directive files.
func MigrateLegacyWorkspace(agentsDir string) error {
	if agentsDir == "" {
		return nil
	}
	targetWorkspace := ResolveAgentWorkspace(agentsDir, "agyent")
	legacyWorkspace := filepath.Join(agentsDir, "agents", "workspace")

	// If legacy workspace exists and target workspace is missing IDENTITY.md
	if info, err := os.Stat(legacyWorkspace); err == nil && info.IsDir() {
		targetIdentity := filepath.Join(targetWorkspace, "IDENTITY.md")
		if _, err := os.Stat(targetIdentity); os.IsNotExist(err) {
			_ = os.MkdirAll(targetWorkspace, 0755)
			entries, err := os.ReadDir(legacyWorkspace)
			if err == nil {
				for _, entry := range entries {
					src := filepath.Join(legacyWorkspace, entry.Name())
					dst := filepath.Join(targetWorkspace, entry.Name())
					if !entry.IsDir() {
						if _, err := os.Stat(dst); os.IsNotExist(err) {
							if data, err := os.ReadFile(src); err == nil {
								_ = os.WriteFile(dst, data, 0644)
							}
						}
					}
				}
			}
		}
	}
	return nil
}

// ExpandPath expands leading ~ with the user's home directory.
func ExpandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home dir: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
			path = filepath.Join(home, path[2:])
		}
	}
	return filepath.Clean(path), nil
}

// ExpandPaths resolves all relative ~ paths in configuration to absolute paths.
func (c *Config) ExpandPaths() error {
	var err error
	if c.Storage.DBPath, err = ExpandPath(c.Storage.DBPath); err != nil {
		return err
	}
	if c.Storage.AgentsDir, err = ExpandPath(c.Storage.AgentsDir); err != nil {
		return err
	}
	if c.AGY.BinaryPath != "" {
		if c.AGY.BinaryPath, err = ExpandPath(c.AGY.BinaryPath); err != nil {
			return err
		}
	}
	for name, a := range c.Agents {
		if a.WorkspacePath != "" {
			if expanded, err := ExpandPath(a.WorkspacePath); err == nil {
				a.WorkspacePath = expanded
				c.Agents[name] = a
			}
		}
	}
	return nil
}

// String returns a redacted string representation of TelegramConfig for safe logging.
func (t TelegramConfig) String() string {
	maskedToken := "***"
	if len(t.BotToken) > 8 {
		maskedToken = t.BotToken[:6] + ":***"
	}
	var maskedBots []string
	for _, b := range t.Bots {
		bMasked := "***"
		if len(b.BotToken) > 8 {
			bMasked = b.BotToken[:6] + ":***"
		}
		maskedBots = append(maskedBots, fmt.Sprintf("{Name: %s, Token: %s, Bind: %s}", b.Name, bMasked, b.BindAgent))
	}
	return fmt.Sprintf("{BotToken: %s, Bots: %v, Mode: %s, AdminUserIDs: %v, AllowedGroupIDs: %v}", maskedToken, maskedBots, t.Mode, t.AdminUserIDs, t.AllowedGroupIDs)
}

// String returns a redacted string representation of ZaloConfig for safe logging.
func (z ZaloConfig) String() string {
	maskedToken := "***"
	if len(z.BotToken) > 8 {
		maskedToken = z.BotToken[:6] + ":***"
	}
	var maskedBots []string
	for _, b := range z.Bots {
		bMasked := "***"
		if len(b.BotToken) > 8 {
			bMasked = b.BotToken[:6] + ":***"
		}
		maskedBots = append(maskedBots, fmt.Sprintf("{Name: %s, Token: %s, Bind: %s}", b.Name, bMasked, b.BindAgent))
	}
	return fmt.Sprintf("{BotToken: %s, Bots: %v, GroupID: %s, Mode: %s, AdminUserIDs: %v, AllowedGroupIDs: %v}", maskedToken, maskedBots, z.GroupID, z.Mode, z.AdminUserIDs, z.AllowedGroupIDs)
}

// String returns a redacted string representation of Config for safe logging.
func (c *Config) String() string {
	if c == nil {
		return "<nil>"
	}
	return fmt.Sprintf("Config{Server: %+v, Telegram: %s, Zalo: %s, AGY: %+v, Storage: %+v, Logging: %+v, Evolution: %+v, Subagent: %+v}",
		c.Server, c.Telegram.String(), c.Zalo.String(), c.AGY, c.Storage, c.Logging, c.Evolution, c.Subagent)
}

// IsAdmin checks whether the given senderID has administrator privileges.
// It encapsulates admin checks across c.Security.AdminUserIDs (canonical gateway admin list)
// and c.Telegram.AdminUserIDs (backward-compatible override).
// Safely parses numeric string IDs while supporting string comparison for non-numeric platforms.
// If no admin IDs are configured anywhere, it fails closed (returns false) unless
// AllowUnauthenticatedLocalDev is explicitly enabled for non-remote channels.
func (c *Config) IsAdmin(senderID string) bool {
	return c.IsAdminForProvider(senderID, "")
}

// IsAdminForProvider checks whether the given senderID has administrator privileges for a specific provider.
// Remote messaging channels (e.g. telegram, discord) ALWAYS fail closed if no admin IDs are configured,
// regardless of AllowUnauthenticatedLocalDev.
func (c *Config) IsAdminForProvider(senderID string, provider string) bool {
	if c == nil || senderID == "" {
		return false
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	adminProvider := provider
	if adminProvider == "" {
		adminProvider = "telegram"
	}
	hasProviderAdmins := len(c.Telegram.AdminUserIDs) > 0
	if adminProvider == "zalo" {
		hasProviderAdmins = len(c.Zalo.AdminUserIDs) > 0
	}
	hasAdmins := len(c.Security.AdminUserIDs) > 0 || hasProviderAdmins
	if !hasAdmins {
		isRemoteProvider := provider == "telegram" || provider == "zalo" || provider == "discord"
		if !isRemoteProvider && c.Security.AllowUnauthenticatedLocalDev {
			return true
		}
		return false
	}
	id, err := strconv.ParseInt(senderID, 10, 64)
	for _, admin := range c.Security.AdminUserIDs {
		if (err == nil && admin == id) || strconv.FormatInt(admin, 10) == senderID {
			return true
		}
	}
	if adminProvider == "telegram" {
		for _, admin := range c.Telegram.AdminUserIDs {
			if (err == nil && admin == id) || strconv.FormatInt(admin, 10) == senderID {
				return true
			}
		}
	}
	if adminProvider == "zalo" {
		for _, admin := range c.Zalo.AdminUserIDs {
			if strings.EqualFold(strings.TrimSpace(admin), strings.TrimSpace(senderID)) {
				return true
			}
		}
	}
	return false
}

// IsSuperAdmin evaluates whether a security principal has global daemon administrative privileges.
func (c *Config) IsSuperAdmin(principal domain.Principal) bool {
	if c == nil {
		return false
	}
	return c.IsAdminForProvider(principal.SubjectID, principal.Provider)
}

// Validate checks required fields and configuration constraints.
func (c *Config) Validate() error {
	normalizedTgBots := c.Telegram.GetNormalizedBots()
	normalizedZaloBots := c.Zalo.GetNormalizedBots()

	hasTelegram := len(normalizedTgBots) > 0 && strings.TrimSpace(normalizedTgBots[0].BotToken) != ""
	hasZalo := len(normalizedZaloBots) > 0 && strings.TrimSpace(normalizedZaloBots[0].BotToken) != ""

	if !hasTelegram && !hasZalo {
		return errors.New("at least one communication channel (telegram or zalo) must be configured with a valid bot token")
	}

	if hasTelegram {
		for _, b := range normalizedTgBots {
			if strings.TrimSpace(b.BotToken) == "" {
				return errors.New("telegram bot token cannot be empty")
			}
		}
		if len(c.Telegram.AdminUserIDs) == 0 {
			return errors.New("at least one telegram admin user ID is required when telegram is configured")
		}
		if c.Telegram.Mode != "polling" && c.Telegram.Mode != "webhook" {
			return fmt.Errorf("invalid telegram mode %q: must be 'polling' or 'webhook'", c.Telegram.Mode)
		}
		if c.Telegram.Mode == "webhook" && strings.TrimSpace(c.Telegram.WebhookURL) == "" {
			return errors.New("telegram webhook_url is required when mode is 'webhook'")
		}
	}

	if hasZalo {
		for _, b := range normalizedZaloBots {
			if strings.TrimSpace(b.BotToken) == "" {
				return errors.New("zalo bot token cannot be empty")
			}
		}
		if c.Zalo.Mode == "" {
			c.Zalo.Mode = "polling"
		}
		if c.Zalo.Mode != "polling" && c.Zalo.Mode != "webhook" {
			return fmt.Errorf("invalid zalo mode %q: must be 'polling' or 'webhook'", c.Zalo.Mode)
		}
		if c.Zalo.Mode == "webhook" && strings.TrimSpace(c.Zalo.WebhookURL) == "" {
			return errors.New("zalo webhook_url is required when mode is 'webhook'")
		}
	}
	if hasTelegram && hasZalo && c.Telegram.Mode == "webhook" && c.Zalo.Mode == "webhook" {
		return errors.New("telegram and zalo cannot both use webhook mode on the shared gateway listener; configure one channel for polling")
	}

	if strings.TrimSpace(c.Storage.DBPath) == "" {
		return errors.New("storage db_path is required")
	}
	if strings.TrimSpace(c.Storage.AgentsDir) == "" {
		return errors.New("storage agents_dir is required")
	}
	if strings.TrimSpace(c.AGY.BinaryPath) == "" {
		return errors.New("agy binary_path is required")
	}
	if c.Storage.DebounceSeconds < 0 {
		return errors.New("storage debounce_seconds cannot be negative")
	}
	if c.Storage.HeartbeatIntervalSeconds < 0 {
		return errors.New("storage heartbeat_interval_seconds cannot be negative")
	}
	if c.Telegram.SecretToken != "" && len(strings.TrimSpace(c.Telegram.SecretToken)) < 16 {
		return errors.New("telegram secret_token must have at least 16 characters for security")
	}
	if c.Zalo.SecretToken != "" && len(strings.TrimSpace(c.Zalo.SecretToken)) < 16 {
		return errors.New("zalo secret_token must have at least 16 characters for security")
	}
	if c.Logging.Level != "" {
		lvl := strings.ToLower(strings.TrimSpace(c.Logging.Level))
		if lvl != "debug" && lvl != "info" && lvl != "warn" && lvl != "warning" && lvl != "error" {
			return fmt.Errorf("invalid logging level %q: must be 'debug', 'info', 'warn', or 'error'", c.Logging.Level)
		}
	}
	if c.Logging.Format != "" {
		fmtStr := strings.ToLower(strings.TrimSpace(c.Logging.Format))
		if fmtStr != "text" && fmtStr != "json" {
			return fmt.Errorf("invalid logging format %q: must be 'text' or 'json'", c.Logging.Format)
		}
	}
	if c.Evolution.ConfidenceThreshold < 0 || c.Evolution.ConfidenceThreshold > 1.0 {
		return errors.New("evolution confidence_threshold must be between 0.0 and 1.0")
	}
	if c.Evolution.IdleTimeoutMinutes < 0 {
		return errors.New("evolution idle_timeout_minutes cannot be negative")
	}
	if c.Evolution.ScanIntervalMinutes < 0 {
		return errors.New("evolution scan_interval_minutes cannot be negative")
	}
	if c.Evolution.ReflectionTimeoutSeconds < 0 {
		return errors.New("evolution reflection_timeout_seconds cannot be negative")
	}
	if c.Subagent.MaxConcurrentWorkers < 0 {
		return errors.New("subagent max_concurrent_workers cannot be negative")
	}
	if c.Subagent.DefaultTimeoutSeconds < 0 {
		return errors.New("subagent default_timeout_seconds cannot be negative")
	}
	for name, a := range c.Agents {
		if a.SecurityPreset != "" {
			switch strings.ToLower(a.SecurityPreset) {
			case "unrestricted", "full_access", "developer", "balanced", "strict", "read_only":
			default:
				return fmt.Errorf("invalid security_preset %q for agent %q: must be unrestricted, developer, balanced, strict, or read_only", a.SecurityPreset, name)
			}
		}
	}
	return nil
}

// Load loads configuration from YAML file and applies environment overrides.
func Load(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	if configPath != "" {
		expandedPath, err := ExpandPath(configPath)
		if err != nil {
			return nil, fmt.Errorf("invalid config path: %w", err)
		}

		if data, err := os.ReadFile(expandedPath); err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("failed to parse yaml config file %s: %w", expandedPath, err)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read config file %s: %w", expandedPath, err)
		}
	}

	// Environment variable overrides
	applyEnvOverrides(cfg)

	// Expand paths
	if err := cfg.ExpandPaths(); err != nil {
		return nil, fmt.Errorf("failed to expand config paths: %w", err)
	}

	return cfg, nil
}

// Save writes the configuration struct to a YAML file.
func Save(configPath string, cfg *Config) error {
	if configPath == "" {
		return errors.New("config path cannot be empty")
	}

	expandedPath, err := ExpandPath(configPath)
	if err != nil {
		return fmt.Errorf("invalid config path: %w", err)
	}

	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory %s: %w", dir, err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to serialize config to YAML: %w", err)
	}

	if err := os.WriteFile(expandedPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file %s: %w", expandedPath, err)
	}

	return nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("AGYENT_SERVER_HOST"); v != "" {
		cfg.Server.Host = v
	}
	if v := os.Getenv("AGYENT_SERVER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = p
		}
	}

	if v := os.Getenv("AGYENT_TELEGRAM_BOT_TOKEN"); v != "" {
		cfg.Telegram.BotToken = v
	}
	if v := os.Getenv("AGYENT_TELEGRAM_MODE"); v != "" {
		cfg.Telegram.Mode = v
	}
	if v := os.Getenv("AGYENT_TELEGRAM_WEBHOOK_URL"); v != "" {
		cfg.Telegram.WebhookURL = v
	}
	if v := os.Getenv("AGYENT_TELEGRAM_ADMIN_USER_IDS"); v != "" {
		cfg.Telegram.AdminUserIDs = parseAdminIDs(v)
	}
	if v := os.Getenv("AGYENT_TELEGRAM_ALLOWED_GROUP_IDS"); v != "" {
		cfg.Telegram.AllowedGroupIDs = parseStringSlice(v)
	}

	if v := os.Getenv("ZALO_BOT_TOKEN"); v != "" {
		cfg.Zalo.BotToken = v
	} else if v := os.Getenv("AGYENT_ZALO_BOT_TOKEN"); v != "" {
		cfg.Zalo.BotToken = v
	}
	if v := os.Getenv("ZALO_GROUP_ID"); v != "" {
		cfg.Zalo.GroupID = v
	} else if v := os.Getenv("AGYENT_ZALO_GROUP_ID"); v != "" {
		cfg.Zalo.GroupID = v
	}
	if v := os.Getenv("ZALO_BOT_ADMIN_ID"); v != "" {
		cfg.Zalo.AdminUserIDs = parseStringSlice(v)
	} else if v := os.Getenv("AGYENT_ZALO_ADMIN_USER_IDS"); v != "" {
		cfg.Zalo.AdminUserIDs = parseStringSlice(v)
	}
	if v := os.Getenv("ZALO_BOT_API_URL"); v != "" {
		cfg.Zalo.APIURL = v
	} else if v := os.Getenv("AGYENT_ZALO_API_URL"); v != "" {
		cfg.Zalo.APIURL = v
	}
	if v := os.Getenv("AGYENT_ZALO_MODE"); v != "" {
		cfg.Zalo.Mode = v
	}
	if v := os.Getenv("AGYENT_ZALO_WEBHOOK_URL"); v != "" {
		cfg.Zalo.WebhookURL = v
	}
	if v := os.Getenv("AGYENT_ZALO_SECRET_TOKEN"); v != "" {
		cfg.Zalo.SecretToken = v
	}
	if v := os.Getenv("AGYENT_ZALO_ALLOWED_GROUP_IDS"); v != "" {
		cfg.Zalo.AllowedGroupIDs = parseStringSlice(v)
	}

	if v := os.Getenv("AGYENT_AGY_BINARY_PATH"); v != "" {
		cfg.AGY.BinaryPath = v
	}
	if v := os.Getenv("AGYENT_AGY_DEFAULT_TIMEOUT_SECONDS"); v != "" {
		if t, err := strconv.Atoi(v); err == nil {
			cfg.AGY.DefaultTimeoutSeconds = t
		}
	}
	if v := os.Getenv("AGYENT_AGY_DEFAULT_MODEL"); v != "" {
		cfg.AGY.DefaultModel = v
	}
	if v := os.Getenv("AGYENT_AGY_DEFAULT_EFFORT"); v != "" {
		cfg.AGY.DefaultEffort = v
	}
	if v := os.Getenv("AGYENT_AGY_DEFAULT_MODE"); v != "" {
		cfg.AGY.DefaultMode = v
	}
	if v := os.Getenv("AGYENT_AGY_DANGEROUSLY_SKIP_PERMISSIONS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.AGY.DangerouslySkipPermissions = b
		}
	}
	if v := os.Getenv("AGYENT_AGY_STREAMING_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.AGY.StreamingEnabled = b
		}
	}
	if v := os.Getenv("AGYENT_AGY_STREAMING_THROTTLE_INTERVAL_SECONDS"); v != "" {
		if d, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.AGY.StreamingThrottleIntervalSeconds = d
		}
	}
	if v := os.Getenv("AGYENT_AGY_AUTO_COMPACT"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.AGY.AutoCompact = b
		}
	}
	if v := os.Getenv("AGYENT_AGY_COMPACT_THRESHOLD_RATIO"); v != "" {
		if d, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.AGY.CompactThresholdRatio = d
		}
	}

	if v := os.Getenv("AGYENT_STORAGE_DB_PATH"); v != "" {

		cfg.Storage.DBPath = v
	}
	if v := os.Getenv("AGYENT_STORAGE_AGENTS_DIR"); v != "" {
		cfg.Storage.AgentsDir = v
	}
	if v := os.Getenv("AGYENT_STORAGE_DEBOUNCE_SECONDS"); v != "" {
		if d, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Storage.DebounceSeconds = d
		}
	}
	if v := os.Getenv("AGYENT_STORAGE_HEARTBEAT_INTERVAL_SECONDS"); v != "" {
		if h, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Storage.HeartbeatIntervalSeconds = h
		}
	}

	if v := os.Getenv("AGYENT_LOGGING_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
	if v := os.Getenv("AGYENT_LOGGING_FORMAT"); v != "" {
		cfg.Logging.Format = v
	}

	if v := os.Getenv("AGYENT_EVOLUTION_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Evolution.Enabled = b
		}
	}
	if v := os.Getenv("AGYENT_EVOLUTION_IDLE_TIMEOUT_MINUTES"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.Evolution.IdleTimeoutMinutes = i
		}
	}
	if v := os.Getenv("AGYENT_EVOLUTION_SCAN_INTERVAL_MINUTES"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.Evolution.ScanIntervalMinutes = i
		}
	}
	if v := os.Getenv("AGYENT_EVOLUTION_CONFIDENCE_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Evolution.ConfidenceThreshold = f
		}
	}
	if v := os.Getenv("AGYENT_EVOLUTION_REFLECTION_TIMEOUT_SECONDS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.Evolution.ReflectionTimeoutSeconds = i
		}
	}
	if v := os.Getenv("AGYENT_EVOLUTION_COMPACTION_LINE_LIMIT"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.Evolution.CompactionLineLimit = i
		}
	}
	if v := os.Getenv("AGYENT_EVOLUTION_QUEUE_CAPACITY"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.Evolution.QueueCapacity = i
		}
	}

	if v := os.Getenv("AGYENT_SUBAGENT_MAX_CONCURRENT_WORKERS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.Subagent.MaxConcurrentWorkers = i
		}
	}
	if v := os.Getenv("AGYENT_SUBAGENT_DEFAULT_TIMEOUT_SECONDS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.Subagent.DefaultTimeoutSeconds = i
		}
	}
	if v := os.Getenv("AGYENT_SUBAGENT_DEFAULT_MODEL"); v != "" {
		cfg.Subagent.DefaultModel = v
	}
	if v := os.Getenv("AGYENT_SUBAGENT_DEFAULT_EFFORT"); v != "" {
		cfg.Subagent.DefaultEffort = v
	}
}

func parseAdminIDs(s string) []int64 {
	var ids []int64
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if id, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func parseStringSlice(s string) []string {
	var list []string
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			list = append(list, trimmed)
		}
	}
	return list
}
