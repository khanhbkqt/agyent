package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServerConfig contains HTTP server configuration.
type ServerConfig struct {
	Host string `yaml:"host" json:"host"`
	Port int    `yaml:"port" json:"port"`
}

// TelegramConfig contains Telegram bot and access control configuration.
type TelegramConfig struct {
	BotToken        string   `yaml:"bot_token" json:"bot_token"`
	Mode            string   `yaml:"mode" json:"mode"` // "polling" or "webhook"
	WebhookURL      string   `yaml:"webhook_url" json:"webhook_url"`
	AdminUserIDs    []int64  `yaml:"admin_user_ids" json:"admin_user_ids"`
	AllowedGroupIDs []string `yaml:"allowed_group_ids" json:"allowed_group_ids"`
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
}

// StorageConfig contains SQLite and filesystem workspace storage configuration.
type StorageConfig struct {
	DBPath                   string  `yaml:"db_path" json:"db_path"`
	AgentsDir                string  `yaml:"agents_dir" json:"agents_dir"`
	DebounceSeconds          float64 `yaml:"debounce_seconds" json:"debounce_seconds"`
	HeartbeatIntervalSeconds float64 `yaml:"heartbeat_interval_seconds" json:"heartbeat_interval_seconds"`
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

// Config represents the complete runtime configuration of agyent.
type Config struct {
	Server    ServerConfig    `yaml:"server" json:"server"`
	Telegram  TelegramConfig  `yaml:"telegram" json:"telegram"`
	AGY       AGYConfig       `yaml:"agy" json:"agy"`
	Storage   StorageConfig   `yaml:"storage" json:"storage"`
	Logging   LoggingConfig   `yaml:"logging" json:"logging"`
	Evolution EvolutionConfig `yaml:"evolution" json:"evolution"`
}

// DefaultConfig returns a new Config populated with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 8080,
		},
		Telegram: TelegramConfig{
			BotToken:        "",
			Mode:            "polling",
			WebhookURL:      "",
			AdminUserIDs:    []int64{},
			AllowedGroupIDs: []string{},
		},
		AGY: AGYConfig{
			BinaryPath:                       "agy",
			DefaultTimeoutSeconds:            300,
			DefaultModel:                     "",
			DefaultEffort:                    "high",
			DefaultMode:                      "accept-edits",
			ModelAliases:                     make(map[string]string),
			DangerouslySkipPermissions:       true,
			StreamingEnabled:                 true,
			StreamingThrottleIntervalSeconds: 1.5,
		},
		Storage: StorageConfig{
			DBPath:                   "~/.agyent/agyent.db",
			AgentsDir:                "~/.agyent",
			DebounceSeconds:          2.0,
			HeartbeatIntervalSeconds: 4.0,
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
	return nil
}

// String returns a redacted string representation of TelegramConfig for safe logging.
func (t TelegramConfig) String() string {
	maskedToken := "***"
	if len(t.BotToken) > 8 {
		maskedToken = t.BotToken[:6] + ":***"
	}
	return fmt.Sprintf("{BotToken: %s, Mode: %s, AdminUserIDs: %v, AllowedGroupIDs: %v}", maskedToken, t.Mode, t.AdminUserIDs, t.AllowedGroupIDs)
}

// String returns a redacted string representation of Config for safe logging.
func (c *Config) String() string {
	if c == nil {
		return "<nil>"
	}
	return fmt.Sprintf("Config{Server: %+v, Telegram: %s, AGY: %+v, Storage: %+v, Logging: %+v, Evolution: %+v}",
		c.Server, c.Telegram.String(), c.AGY, c.Storage, c.Logging, c.Evolution)
}

// Validate checks required fields and configuration constraints.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Telegram.BotToken) == "" {
		return errors.New("telegram bot token is required")
	}
	if len(c.Telegram.AdminUserIDs) == 0 {
		return errors.New("at least one telegram admin user ID is required")
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
	if c.Telegram.Mode != "polling" && c.Telegram.Mode != "webhook" {
		return fmt.Errorf("invalid telegram mode %q: must be 'polling' or 'webhook'", c.Telegram.Mode)
	}
	if c.Telegram.Mode == "webhook" && strings.TrimSpace(c.Telegram.WebhookURL) == "" {
		return errors.New("telegram webhook_url is required when mode is 'webhook'")
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
