package doctor

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"agyent/internal/config"

	_ "modernc.org/sqlite"
	"gopkg.in/yaml.v3"
)

var reUnknownField = regexp.MustCompile(`(?i)line\s+(\d+):\s+field\s+(.+?)\s+not\s+found\s+in\s+type\s+([^\s]+)`)

var scopedDeprecatedFields = map[string]map[string]string{
	"config.AgentProfileConfig": {
		"preset":    "Under agent profiles, use 'security_preset' instead of 'preset'.",
		"workspace": "Under agent profiles, use 'workspace_path' instead of 'workspace'.",
	},
	"config.CommandGuardrailConfig": {
		"whitelist_patterns": "Use 'custom_whitelist' under 'security.commands' instead.",
		"blacklist_patterns": "Use 'custom_blacklist' under 'security.commands' instead.",
	},
	"config.SubagentConfig": {
		"max_workers": "Use 'max_concurrent_workers' under 'subagent' instead.",
	},
	"config.SubagentGuardrailConfig": {
		"max_workers": "Use 'max_concurrent_workers' under 'security.subagents' instead.",
	},
}

var knownConfigFields = []string{
	// Root
	"server", "telegram", "zalo", "agy", "storage", "scheduler",
	"logging", "evolution", "subagent", "security", "recovery", "agents",
	// Server
	"host", "port",
	// Channels
	"bot_token", "bots", "bind_agent", "mode", "webhook_url", "secret_token",
	"admin_user_ids", "allowed_group_ids", "group_id", "api_url", "name",
	// AGY
	"binary_path", "default_timeout_seconds", "default_model", "default_effort",
	"default_mode", "model_aliases", "dangerously_skip_permissions",
	"streaming_enabled", "streaming_throttle_interval_seconds", "auto_compact",
	"compact_threshold_ratio", "queue_mode", "append_strategy", "grace_timeout_seconds",
	// Storage
	"db_path", "agents_dir", "debounce_seconds", "heartbeat_interval_seconds",
	// Scheduler
	"enabled", "poll_interval_seconds", "default_task_timeout_seconds", "heartbeat_timeout_seconds",
	// Logging
	"level", "format",
	// Evolution
	"idle_timeout_minutes", "scan_interval_minutes", "confidence_threshold",
	"reflection_timeout_seconds", "compaction_line_limit", "queue_capacity",
	// Subagent
	"max_concurrent_workers",
	// Security
	"preset", "approval_timeout_seconds", "allow_unauthenticated_local_dev",
	"allowed_project_roots", "agent_config_management", "commands", "filesystem",
	"subagents", "network", "dlp",
	// AgentConfigManagement
	"require_approval", "manageable_files",
	// Commands Guardrail
	"custom_blacklist", "custom_whitelist",
	// Filesystem Guardrail
	"enforce_workspace_jail", "allowed_paths", "forbidden_paths",
	// Subagent Guardrail
	"max_cascade_depth", "roles", "allowed_tools", "disallowed_tools",
	// Network Guardrail
	"block_cloud_metadata", "block_private_networks", "prevent_dns_rebinding",
	// DLP Guardrail
	"redaction_mode", "sliding_window_bytes", "sanitize_tool_outputs", "whitelisted_env_keys",
	// Recovery
	"max_retries", "max_concurrent_recoveries", "retention_days", "subagents_auto_resume",
	// Agent Profile
	"security_preset", "workspace_path", "description", "is_public", "owner_id",
}

// CheckConfiguration validates configuration file existence, syntax, strict schema keys, and business constraints.
func (d *DoctorRunner) CheckConfiguration(ctx ...context.Context) ([]CheckResult, *config.Config) {
	var results []CheckResult
	var checkCtx context.Context = context.Background()
	if len(ctx) > 0 && ctx[0] != nil {
		checkCtx = ctx[0]
	}

	expandedConfigPath, err := config.ExpandPath(d.opts.ConfigPath)
	if err != nil {
		results = append(results, CheckResult{
			Name:        "Config Path Expansion",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Failed to expand configuration path %q: %v", d.opts.ConfigPath, err),
			Remediation: "Check configuration path syntax.",
		})
		return results, nil
	}

	// 1. Config File Existence
	rawBytes, err := os.ReadFile(expandedConfigPath)
	if os.IsNotExist(err) {
		results = append(results, CheckResult{
			Name:        "Configuration File Existence",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Configuration file not found at %s", expandedConfigPath),
			Remediation: "Run 'agyent init' to create initial configuration interactively, or copy example config.",
			CanAutoFix:  true,
		})
		return results, nil
	} else if err != nil {
		results = append(results, CheckResult{
			Name:        "Configuration File Read",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Failed to read configuration file at %s: %v", expandedConfigPath, err),
			Remediation: "Check file permissions for configuration file.",
		})
		return results, nil
	}

	results = append(results, CheckResult{
		Name:     "Configuration File Existence",
		Category: CategoryConfig,
		Status:   StatusPass,
		Message:  fmt.Sprintf("Found configuration file at %s", expandedConfigPath),
	})

	// 2. Strict YAML Schema & Typo Linting
	lintResults := checkConfigStrictLinting(rawBytes)
	results = append(results, lintResults...)

	// 3. Load and parse YAML normally with defaults and env overrides
	cfg, err := config.Load(d.opts.ConfigPath)
	if err != nil {
		results = append(results, CheckResult{
			Name:        "YAML Syntax & Parsing",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Failed to parse configuration YAML: %v", err),
			Remediation: "Fix syntax errors in YAML file or run 'agyent init --non-interactive' to re-generate.",
		})
		return results, nil
	}

	results = append(results, CheckResult{
		Name:     "YAML Syntax & Parsing",
		Category: CategoryConfig,
		Status:   StatusPass,
		Message:  "Configuration YAML parsed successfully",
	})

	// 4. Structural Validation
	if err := cfg.Validate(); err != nil {
		results = append(results, CheckResult{
			Name:        "Configuration Schema Validation",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Configuration validation failed: %v", err),
			Remediation: "Check missing or invalid fields in config file.",
		})
	} else {
		results = append(results, CheckResult{
			Name:     "Configuration Schema Validation",
			Category: CategoryConfig,
			Status:   StatusPass,
			Message:  "All required configuration constraints satisfied",
		})
	}

	// 5. Deep Semantic & Format Validation (RegEx, Paths, Enums)
	semanticResults := checkConfigSemantics(cfg)
	results = append(results, semanticResults...)

	// 6. Security Posture Audit
	securityPostureResults := checkConfigSecurityPosture(cfg)
	results = append(results, securityPostureResults...)

	// 7. Channel Configuration & Redundancy Checks
	channelResults := checkChannelConfigurations(cfg)
	results = append(results, channelResults...)

	// 8. Agent Profile Drift Detection against SQLite
	driftResults := d.checkAgentDrift(checkCtx, cfg)
	results = append(results, driftResults...)

	return results, cfg
}

func checkConfigStrictLinting(rawBytes []byte) []CheckResult {
	var results []CheckResult
	dec := yaml.NewDecoder(bytes.NewReader(rawBytes))
	dec.KnownFields(true)

	var strictCfg config.Config
	err := dec.Decode(&strictCfg)
	if err == nil {
		results = append(results, CheckResult{
			Name:     "Configuration Schema & Field Linting",
			Category: CategoryConfig,
			Status:   StatusPass,
			Message:  "All configuration keys are recognized and strictly validated",
		})
		return results
	}

	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		for _, errMsg := range typeErr.Errors {
			matches := reUnknownField.FindStringSubmatch(errMsg)
			if len(matches) == 4 {
				lineStr := matches[1]
				field := matches[2]
				structType := matches[3]

				// Check if it's a known deprecated field on this specific struct type
				if structDeps, ok := scopedDeprecatedFields[structType]; ok {
					if advice, deprecated := structDeps[field]; deprecated {
						results = append(results, CheckResult{
							Name:        "Deprecated Configuration Key",
							Category:    CategoryConfig,
							Status:      StatusWarn,
							Message:     fmt.Sprintf("Found deprecated configuration key %q at line %s: %s", field, lineStr, advice),
							Remediation: advice,
						})
						continue
					}
				}

				// Find closest known field for typo suggestion
				closest, dist := findClosestField(field, knownConfigFields)
				msg := fmt.Sprintf("Unknown configuration field %q at line %s", field, lineStr)
				remediation := "Check your config.yaml against the official schema documentation."
				if closest != "" && dist <= 3 {
					msg = fmt.Sprintf("Unknown configuration field %q at line %s (Did you mean %q?)", field, lineStr, closest)
					remediation = fmt.Sprintf("Replace %q with %q in config.yaml.", field, closest)
				}

				results = append(results, CheckResult{
					Name:        "Configuration Key Linting",
					Category:    CategoryConfig,
					Status:      StatusFail,
					Message:     msg,
					Remediation: remediation,
				})
			} else {
				results = append(results, CheckResult{
					Name:        "Configuration Field Type Linting",
					Category:    CategoryConfig,
					Status:      StatusFail,
					Message:     fmt.Sprintf("Configuration type mismatch: %s", errMsg),
					Remediation: "Verify the expected data type for this configuration field.",
				})
			}
		}
	} else {
		results = append(results, CheckResult{
			Name:        "YAML Syntax Linting",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     fmt.Sprintf("YAML syntax error: %v", err),
			Remediation: "Fix YAML indentation and syntax errors.",
		})
	}

	return results
}

func checkConfigSemantics(cfg *config.Config) []CheckResult {
	var results []CheckResult
	if cfg == nil {
		return results
	}

	// 1. RegEx Validation on custom_blacklist & custom_whitelist
	patternCount := 0
	for _, pat := range cfg.Security.Commands.CustomBlacklist {
		patternCount++
		if _, err := regexp.Compile(pat); err != nil {
			results = append(results, CheckResult{
				Name:        "Command Blacklist RegEx",
				Category:    CategoryConfig,
				Status:      StatusFail,
				Message:     fmt.Sprintf("Invalid regular expression in security.commands.custom_blacklist: %q: %v", pat, err),
				Remediation: "Ensure valid Go regular expression syntax (e.g. `(?i)rm\\s+-rf`).",
			})
		}
	}
	for _, pat := range cfg.Security.Commands.CustomWhitelist {
		if pat == "*" {
			continue
		}
		patternCount++
		if _, err := regexp.Compile(pat); err != nil {
			results = append(results, CheckResult{
				Name:        "Command Whitelist RegEx",
				Category:    CategoryConfig,
				Status:      StatusFail,
				Message:     fmt.Sprintf("Invalid regular expression in security.commands.custom_whitelist: %q: %v", pat, err),
				Remediation: "Ensure valid Go regular expression syntax.",
			})
		}
	}
	if patternCount > 0 && len(results) == 0 {
		results = append(results, CheckResult{
			Name:     "Command Guardrail RegEx Validation",
			Category: CategoryConfig,
			Status:   StatusPass,
			Message:  fmt.Sprintf("Successfully validated %d custom command guardrail pattern(s)", patternCount),
		})
	}

	// 2. Filesystem Paths & Forbidden Path Collisions
	forbiddenPaths := []string{
		"~/.ssh",
		"~/.aws",
		"~/.kube",
		".agents/hooks.json",
		"~/.gemini/config/hooks.json",
	}
	if strings.TrimSpace(cfg.Storage.DBPath) != "" {
		forbiddenPaths = append(forbiddenPaths, cfg.Storage.DBPath)
	} else {
		forbiddenPaths = append(forbiddenPaths, "~/.agyent/agyent.db")
	}
	for _, fp := range cfg.Security.Filesystem.ForbiddenPaths {
		if fp != "" {
			forbiddenPaths = append(forbiddenPaths, fp)
		}
	}

	type pathSource struct {
		path        string
		source      string
		presetName  string
		isWorkspace bool
	}
	var pathsToCheck []pathSource
	for _, p := range cfg.Security.Filesystem.AllowedPaths {
		if p != "" {
			pathsToCheck = append(pathsToCheck, pathSource{
				path:       p,
				source:     "security.filesystem.allowed_paths",
				presetName: cfg.Security.Preset,
			})
		}
	}
	for name, a := range cfg.Agents {
		agentPreset := cfg.ResolveAgentPreset(name)
		for _, p := range a.AllowedPaths {
			if p != "" {
				pathsToCheck = append(pathsToCheck, pathSource{
					path:       p,
					source:     fmt.Sprintf("agents.%s.allowed_paths", name),
					presetName: agentPreset,
				})
			}
		}
		if a.WorkspacePath != "" {
			pathsToCheck = append(pathsToCheck, pathSource{
				path:        a.WorkspacePath,
				source:      fmt.Sprintf("agents.%s.workspace_path", name),
				presetName:  agentPreset,
				isWorkspace: true,
			})
		}
	}

	for _, ps := range pathsToCheck {
		p := ps.path
		if p == "*" {
			effectivePreset := ps.presetName
			if effectivePreset == "" {
				effectivePreset = cfg.Security.Preset
			}
			if effectivePreset == "" {
				effectivePreset = "balanced"
			}
			if strings.ToLower(effectivePreset) != "unrestricted" && strings.ToLower(effectivePreset) != "full_access" {
				results = append(results, CheckResult{
					Name:        "Filesystem Wildcard Warning",
					Category:    CategoryConfig,
					Status:      StatusWarn,
					Message:     fmt.Sprintf("Wildcard allowed_path '*' in %s grants unrestricted filesystem access under non-unrestricted preset %q", ps.source, effectivePreset),
					Remediation: "Explicitly specify allowed directories instead of wildcard '*'.",
				})
			}
			continue
		}

		if p != "." && !filepath.IsAbs(p) && !strings.HasPrefix(p, "~") {
			results = append(results, CheckResult{
				Name:        "Relative Allowed Path",
				Category:    CategoryConfig,
				Status:      StatusWarn,
				Message:     fmt.Sprintf("Path %q in %s is relative; absolute paths are recommended for determinism", p, ps.source),
				Remediation: "Use an absolute path or prefix with '~/' for user home directory.",
			})
		}

		expAllowed, err := config.ExpandPath(p)
		if err != nil {
			results = append(results, CheckResult{
				Name:        "Path Resolution Error",
				Category:    CategoryConfig,
				Status:      StatusFail,
				Message:     fmt.Sprintf("Failed to resolve path %q in %s: %v", p, ps.source, err),
				Remediation: "Check path syntax in config.yaml.",
			})
			continue
		}
		cleanAllowed := filepath.Clean(expAllowed)
		if !filepath.IsAbs(cleanAllowed) {
			if abs, err := filepath.Abs(cleanAllowed); err == nil {
				cleanAllowed = abs
			}
		}

		// Direct hook file collision detection
		if isHookConfigFile(cleanAllowed) {
			results = append(results, CheckResult{
				Name:        "Forbidden Hook File Collision",
				Category:    CategoryConfig,
				Status:      StatusFail,
				Message:     fmt.Sprintf("Security violation: path %q in %s exposes immutable security hooks configuration", p, ps.source),
				Remediation: "Remove hooks file path from allowed_paths/workspace_path to prevent bypass.",
			})
		}

		// Cross-check against forbidden paths
		for _, fp := range forbiddenPaths {
			expFP, err := config.ExpandPath(fp)
			if err != nil {
				continue
			}
			cleanFP := filepath.Clean(expFP)
			if !filepath.IsAbs(cleanFP) {
				if abs, err := filepath.Abs(cleanFP); err == nil {
					cleanFP = abs
				}
			}

			if cleanAllowed == cleanFP {
				results = append(results, CheckResult{
					Name:        "Forbidden Path Collision",
					Category:    CategoryConfig,
					Status:      StatusFail,
					Message:     fmt.Sprintf("Security violation: path %q in %s directly exposes forbidden path %q", p, ps.source, fp),
					Remediation: fmt.Sprintf("Remove %q; access to %s is strictly forbidden by policy.", p, fp),
				})
			} else if isSubpath(cleanAllowed, cleanFP) {
				results = append(results, CheckResult{
					Name:        "Forbidden Subpath Collision",
					Category:    CategoryConfig,
					Status:      StatusFail,
					Message:     fmt.Sprintf("Security violation: path %q in %s is located inside forbidden path %q", p, ps.source, fp),
					Remediation: fmt.Sprintf("Remove %q; it violates forbidden directory isolation.", p),
				})
			} else if cleanAllowed != "." && isSubpath(cleanFP, cleanAllowed) {
				home, _ := os.UserHomeDir()
				if cleanAllowed == "/" || cleanAllowed == home {
					results = append(results, CheckResult{
						Name:        "Broad Allowed Path",
						Category:    CategoryConfig,
						Status:      StatusWarn,
						Message:     fmt.Sprintf("Broad path %q in %s subsumes forbidden directory %q; runtime blacklist will enforce protection", p, ps.source, fp),
						Remediation: "Narrow down paths to specific project workspaces.",
					})
				} else {
					results = append(results, CheckResult{
						Name:        "Subsuming Allowed Path",
						Category:    CategoryConfig,
						Status:      StatusWarn,
						Message:     fmt.Sprintf("Path %q in %s subsumes forbidden path %q; access to nested forbidden paths will be blocked at runtime", p, ps.source, fp),
						Remediation: fmt.Sprintf("Ensure path %q does not grant unauthorized access to %s.", p, fp),
					})
				}
			}
		}

		// Existence check
		if p != "." {
			if _, err := os.Stat(cleanAllowed); os.IsNotExist(err) {
				checkName := "Missing Allowed Directory"
				if ps.isWorkspace {
					checkName = "Missing Workspace Directory"
				}
				results = append(results, CheckResult{
					Name:        checkName,
					Category:    CategoryConfig,
					Status:      StatusWarn,
					Message:     fmt.Sprintf("Configured path %q in %s does not exist on disk (%s)", p, ps.source, cleanAllowed),
					Remediation: fmt.Sprintf("Create directory %s or verify path spelling.", cleanAllowed),
				})
			}
		}
	}

	// 3. Enum Constraints
	if val := strings.ToLower(strings.TrimSpace(cfg.AGY.DefaultEffort)); val != "" {
		if val != "low" && val != "medium" && val != "high" && val != "none" {
			results = append(results, CheckResult{
				Name:        "AGY Default Effort Enum",
				Category:    CategoryConfig,
				Status:      StatusFail,
				Message:     fmt.Sprintf("Invalid agy.default_effort %q: must be 'low', 'medium', 'high', or 'none'", cfg.AGY.DefaultEffort),
				Remediation: "Set agy.default_effort to 'low', 'medium', 'high', or 'none'.",
			})
		}
	}
	if val := strings.ToLower(strings.TrimSpace(cfg.AGY.DefaultMode)); val != "" {
		if val != "accept-edits" && val != "plan" && val != "bypass-permissions" {
			results = append(results, CheckResult{
				Name:        "AGY Default Mode Enum",
				Category:    CategoryConfig,
				Status:      StatusFail,
				Message:     fmt.Sprintf("Invalid agy.default_mode %q: must be 'accept-edits', 'plan', or 'bypass-permissions'", cfg.AGY.DefaultMode),
				Remediation: "Set agy.default_mode to 'accept-edits', 'plan', or 'bypass-permissions'.",
			})
		}
	}
	if val := strings.ToLower(strings.TrimSpace(cfg.AGY.QueueMode)); val != "" {
		if val != "fifo" && val != "append" {
			results = append(results, CheckResult{
				Name:        "AGY Queue Mode Enum",
				Category:    CategoryConfig,
				Status:      StatusFail,
				Message:     fmt.Sprintf("Invalid agy.queue_mode %q: must be 'fifo' or 'append'", cfg.AGY.QueueMode),
				Remediation: "Set agy.queue_mode to 'fifo' or 'append'.",
			})
		}
	}
	if val := strings.ToLower(strings.TrimSpace(cfg.AGY.AppendStrategy)); val != "" {
		if val != "coalesce" && val != "replace" {
			results = append(results, CheckResult{
				Name:        "AGY Append Strategy Enum",
				Category:    CategoryConfig,
				Status:      StatusFail,
				Message:     fmt.Sprintf("Invalid agy.append_strategy %q: must be 'coalesce' or 'replace'", cfg.AGY.AppendStrategy),
				Remediation: "Set agy.append_strategy to 'coalesce' or 'replace'.",
			})
		}
	}
	if val := strings.ToLower(strings.TrimSpace(cfg.Security.Preset)); val != "" {
		switch val {
		case "unrestricted", "full_access", "developer", "balanced", "strict", "read_only", "workspace_only", "workspace":
		default:
			results = append(results, CheckResult{
				Name:        "Security Preset Enum",
				Category:    CategoryConfig,
				Status:      StatusFail,
				Message:     fmt.Sprintf("Invalid security.preset %q: must be developer, balanced, strict, read_only, workspace_only, or unrestricted", cfg.Security.Preset),
				Remediation: "Set security.preset to a recognized preset name.",
			})
		}
	}
	if val := strings.ToLower(strings.TrimSpace(cfg.Security.DLP.RedactionMode)); val != "" {
		if val != "strict" && val != "permissive" && val != "audit_only" {
			results = append(results, CheckResult{
				Name:        "DLP Redaction Mode Enum",
				Category:    CategoryConfig,
				Status:      StatusFail,
				Message:     fmt.Sprintf("Invalid security.dlp.redaction_mode %q: must be 'strict', 'permissive', or 'audit_only'", cfg.Security.DLP.RedactionMode),
				Remediation: "Set security.dlp.redaction_mode to 'strict', 'permissive', or 'audit_only'.",
			})
		}
	}

	for name, a := range cfg.Agents {
		if val := strings.ToLower(strings.TrimSpace(a.SecurityPreset)); val != "" {
			switch val {
			case "unrestricted", "full_access", "developer", "balanced", "strict", "read_only", "workspace_only", "workspace":
			default:
				results = append(results, CheckResult{
					Name:        fmt.Sprintf("Agent %s Security Preset Enum", name),
					Category:    CategoryConfig,
					Status:      StatusFail,
					Message:     fmt.Sprintf("Invalid security_preset %q for agent %q", a.SecurityPreset, name),
					Remediation: "Specify developer, balanced, strict, read_only, workspace_only, or unrestricted.",
				})
			}
		}
		if val := strings.ToLower(strings.TrimSpace(a.DefaultEffort)); val != "" {
			if val != "low" && val != "medium" && val != "high" && val != "none" {
				results = append(results, CheckResult{
					Name:        fmt.Sprintf("Agent %s Default Effort Enum", name),
					Category:    CategoryConfig,
					Status:      StatusFail,
					Message:     fmt.Sprintf("Invalid default_effort %q for agent %q: must be 'low', 'medium', 'high', or 'none'", a.DefaultEffort, name),
					Remediation: "Specify 'low', 'medium', 'high', or 'none'.",
				})
			}
		}
		if a.WorkspacePath != "" {
			expWS, err := config.ExpandPath(a.WorkspacePath)
			if err != nil {
				results = append(results, CheckResult{
					Name:        fmt.Sprintf("Agent %s Workspace Path Resolution", name),
					Category:    CategoryConfig,
					Status:      StatusFail,
					Message:     fmt.Sprintf("Failed to resolve workspace_path %q for agent %q: %v", a.WorkspacePath, name, err),
					Remediation: "Check workspace_path syntax in config.yaml.",
				})
			} else if _, err := os.Stat(expWS); os.IsNotExist(err) {
				results = append(results, CheckResult{
					Name:        fmt.Sprintf("Agent %s Workspace Existence", name),
					Category:    CategoryConfig,
					Status:      StatusWarn,
					Message:     fmt.Sprintf("Workspace path %q for agent %q does not exist on disk (%s)", a.WorkspacePath, name, expWS),
					Remediation: fmt.Sprintf("Create directory %s or verify configuration.", expWS),
				})
			}
		}
	}

	return results
}

func checkConfigSecurityPosture(cfg *config.Config) []CheckResult {
	var results []CheckResult
	if cfg == nil {
		return results
	}

	if cfg.AGY.DangerouslySkipPermissions {
		results = append(results, CheckResult{
			Name:        "AGY Permission Bypass Warning",
			Category:    CategoryConfig,
			Status:      StatusWarn,
			Message:     "AGY 'dangerously_skip_permissions' is enabled; execution confirmation prompts are bypassed",
			Remediation: "Disable 'dangerously_skip_permissions' in production environments.",
		})
	}

	if !cfg.Security.Enabled {
		results = append(results, CheckResult{
			Name:        "Security Gateway Disabled",
			Category:    CategoryConfig,
			Status:      StatusWarn,
			Message:     "Security Gateway is disabled ('security.enabled: false'); commands and tools run unmonitored",
			Remediation: "Set 'security.enabled: true' to enforce guardrails and workspace isolation.",
		})
	}

	preset := strings.ToLower(strings.TrimSpace(cfg.Security.Preset))
	if preset == "unrestricted" || preset == "full_access" {
		results = append(results, CheckResult{
			Name:        "Unrestricted Security Preset",
			Category:    CategoryConfig,
			Status:      StatusWarn,
			Message:     fmt.Sprintf("Global security preset is set to %q; guardrails and filesystem jailing are inactive", cfg.Security.Preset),
			Remediation: "Change security.preset to 'developer' or 'balanced' for safe operations.",
		})
	}

	for name, a := range cfg.Agents {
		aPreset := strings.ToLower(strings.TrimSpace(a.SecurityPreset))
		if aPreset == "unrestricted" || aPreset == "full_access" {
			results = append(results, CheckResult{
				Name:        fmt.Sprintf("Agent %s Unrestricted Preset", name),
				Category:    CategoryConfig,
				Status:      StatusWarn,
				Message:     fmt.Sprintf("Agent %q security preset is %q; guardrails and workspace jailing are inactive for this agent", name, a.SecurityPreset),
				Remediation: fmt.Sprintf("Change agents.%s.security_preset to 'developer' or 'balanced' for safe operations.", name),
			})
		}
	}

	return results
}

func checkChannelConfigurations(cfg *config.Config) []CheckResult {
	var results []CheckResult
	if cfg == nil {
		return results
	}

	// Telegram Admin Whitelist check
	if len(cfg.Telegram.AdminUserIDs) == 0 && len(cfg.Telegram.GetNormalizedBots()) > 0 {
		results = append(results, CheckResult{
			Name:        "Telegram Admin Whitelist",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     "No admin user IDs configured (admin_user_ids is empty)",
			Remediation: "Specify at least one numeric Telegram User ID under 'telegram.admin_user_ids'.",
		})
	} else if len(cfg.Telegram.AdminUserIDs) > 0 {
		results = append(results, CheckResult{
			Name:     "Telegram Admin Whitelist",
			Category: CategoryConfig,
			Status:   StatusPass,
			Message:  fmt.Sprintf("%d admin user ID(s) configured: %v", len(cfg.Telegram.AdminUserIDs), cfg.Telegram.AdminUserIDs),
		})
	}

	// Telegram Bot Token Count & Mode
	tgBots := cfg.Telegram.GetNormalizedBots()
	if len(tgBots) == 0 && len(cfg.Zalo.GetNormalizedBots()) == 0 {
		results = append(results, CheckResult{
			Name:        "Communication Channel Bots",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     "No bot token configured for Telegram or Zalo",
			Remediation: "Configure bot token in 'telegram.bot_token', 'telegram.bots', 'zalo.bot_token', or 'zalo.bots'.",
		})
	} else if len(tgBots) > 0 {
		var botNames []string
		for _, b := range tgBots {
			botNames = append(botNames, b.Name)
		}
		results = append(results, CheckResult{
			Name:     "Telegram Bot Configuration",
			Category: CategoryConfig,
			Status:   StatusPass,
			Message:  fmt.Sprintf("%d bot instance(s) configured [%s] (Mode: %s)", len(tgBots), strings.Join(botNames, ", "), cfg.Telegram.Mode),
		})
	}

	// Telegram Token Redundancy Warning
	if strings.TrimSpace(cfg.Telegram.BotToken) != "" && len(cfg.Telegram.Bots) > 0 {
		results = append(results, CheckResult{
			Name:        "Telegram Token Redundancy",
			Category:    CategoryConfig,
			Status:      StatusWarn,
			Message:     "Telegram config defines both top-level 'bot_token' and multi-bot 'bots' list; top-level token is redundant",
			Remediation: "Remove top-level 'telegram.bot_token' when using 'telegram.bots'.",
		})
	}

	// Zalo Token Redundancy Warning
	if strings.TrimSpace(cfg.Zalo.BotToken) != "" && len(cfg.Zalo.Bots) > 0 {
		results = append(results, CheckResult{
			Name:        "Zalo Token Redundancy",
			Category:    CategoryConfig,
			Status:      StatusWarn,
			Message:     "Zalo config defines both top-level 'bot_token' and multi-bot 'bots' list; top-level token is redundant",
			Remediation: "Remove top-level 'zalo.bot_token' when using 'zalo.bots'.",
		})
	}

	// Zalo Admin Whitelist check
	if len(cfg.Zalo.GetNormalizedBots()) > 0 {
		if len(cfg.Zalo.AdminUserIDs) == 0 {
			results = append(results, CheckResult{
				Name:        "Zalo Admin Whitelist",
				Category:    CategoryConfig,
				Status:      StatusWarn,
				Message:     "Zalo has no admin_user_ids configured; only global security admins will have approval privileges",
				Remediation: "Configure 'zalo.admin_user_ids' with admin user ID strings.",
			})
		} else {
			results = append(results, CheckResult{
				Name:     "Zalo Admin Whitelist",
				Category: CategoryConfig,
				Status:   StatusPass,
				Message:  fmt.Sprintf("%d Zalo admin user ID(s) configured", len(cfg.Zalo.AdminUserIDs)),
			})
		}
	}

	return results
}

func (d *DoctorRunner) checkAgentDrift(ctx context.Context, cfg *config.Config) []CheckResult {
	var results []CheckResult
	if cfg == nil || cfg.Storage.DBPath == "" {
		return results
	}

	expandedDBPath, err := config.ExpandPath(cfg.Storage.DBPath)
	if err != nil {
		return results
	}

	if _, err := os.Stat(expandedDBPath); os.IsNotExist(err) {
		return results
	}

	db, err := sql.Open("sqlite", expandedDBPath+"?_pragma=busy_timeout(3000)&_pragma=query_only(true)")
	if err != nil {
		return results
	}
	defer db.Close()

	var tableCount int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agents'").Scan(&tableCount)
	if err != nil || tableCount == 0 {
		return results
	}

	rows, err := db.QueryContext(ctx, "SELECT name, security_preset FROM agents")
	if err != nil {
		return results
	}
	defer rows.Close()

	type dbAgentInfo struct {
		name           string
		securityPreset string
	}
	dbAgents := make(map[string]dbAgentInfo)
	for rows.Next() {
		var a dbAgentInfo
		if err := rows.Scan(&a.name, &a.securityPreset); err == nil {
			dbAgents[a.name] = a
		}
	}

	for name, agentCfg := range cfg.Agents {
		dba, exists := dbAgents[name]
		if !exists {
			results = append(results, CheckResult{
				Name:     fmt.Sprintf("Agent Registration: %s", name),
				Category: CategoryConfig,
				Status:   StatusInfo,
				Message:  fmt.Sprintf("Agent %q is declared in config.yaml but not yet provisioned in SQLite (will be auto-provisioned on startup)", name),
			})
			continue
		}

		if agentCfg.SecurityPreset != "" && dba.securityPreset != "" &&
			!strings.EqualFold(agentCfg.SecurityPreset, dba.securityPreset) {
			results = append(results, CheckResult{
				Name:        fmt.Sprintf("Agent Preset Drift: %s", name),
				Category:    CategoryConfig,
				Status:      StatusWarn,
				Message:     fmt.Sprintf("Agent %q preset mismatch: config.yaml specifies %q, but database records %q", name, agentCfg.SecurityPreset, dba.securityPreset),
				Remediation: fmt.Sprintf("Run 'agyent agent update %s --preset %s' to synchronize database with config.yaml.", name, agentCfg.SecurityPreset),
			})
		}
	}

	return results
}

func findClosestField(field string, knownFields []string) (string, int) {
	fieldLower := strings.ToLower(field)
	bestMatch := ""
	bestDist := 999

	for _, k := range knownFields {
		kLower := strings.ToLower(k)
		if fieldLower == kLower {
			return k, 0
		}
		d := levenshtein(fieldLower, kLower)
		if d < bestDist {
			bestDist = d
			bestMatch = k
		}
	}
	return bestMatch, bestDist
}

func levenshtein(a, b string) int {
	la := len(a)
	lb := len(b)
	d := make([][]int, la+1)
	for i := range d {
		d[i] = make([]int, lb+1)
		d[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		d[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(
				d[i-1][j]+1,      // deletion
				d[i][j-1]+1,      // insertion
				d[i-1][j-1]+cost, // substitution
			)
		}
	}
	return d[la][lb]
}

func isSubpath(target, base string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "."
}

func isHookConfigFile(path string) bool {
	norm := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	return strings.HasSuffix(norm, "/.agents/hooks.json") ||
		norm == ".agents/hooks.json" ||
		strings.HasSuffix(norm, "/.gemini/config/hooks.json") ||
		norm == ".gemini/config/hooks.json" ||
		filepath.Base(norm) == "hooks.json"
}

