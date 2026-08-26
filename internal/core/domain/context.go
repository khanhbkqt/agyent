package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ContextScope defines the isolation level of a configuration or capability.
type ContextScope string

const (
	ScopeGlobal    ContextScope = "global"
	ScopeWorkspace ContextScope = "workspace"
)

// SkillHeader represents the lightweight summary metadata of a skill for Progressive Disclosure.
type SkillHeader struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	FilePath    string       `json:"file_path"`
	Scope       ContextScope `json:"scope"`
}

// MCPServerConfig defines the configuration for Model Context Protocol (MCP) server execution.
type MCPServerConfig struct {
	ServerName string            `json:"server_name,omitempty"`
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	ServerURL  string            `json:"server_url,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Scope      ContextScope      `json:"scope,omitempty"`
	Disabled   bool              `json:"disabled,omitempty"`
}

// ResolvedContext represents the fully merged runtime context package injected into a turn execution.
type ResolvedContext struct {
	WorkingDir          string            `json:"working_dir"`
	GlobalDirectives    string            `json:"global_directives,omitempty"`
	WorkspaceDirectives string            `json:"workspace_directives,omitempty"`
	CombinedDirectives  string            `json:"combined_directives"`
	SkillHeaders        []SkillHeader     `json:"skill_headers,omitempty"`
	ActiveMCPServers    []MCPServerConfig `json:"active_mcp_servers,omitempty"`
	ActivePlugins       []Plugin          `json:"active_plugins,omitempty"`
	UserLocation        *time.Location    `json:"user_location,omitempty"`
	ResolvedAt          time.Time         `json:"resolved_at"`
}

var (
	ianaTzRegex   = regexp.MustCompile(`\b([A-Za-z]{3,12}/[A-Za-z0-9_+-]+)\b`)
	offsetTzRegex = regexp.MustCompile(`(?i)\b(?:UTC|GMT)\s*([+-])\s*(\d{1,2})(?::?(\d{2}))?\b`)
)

// ParseLocationFromText scans text for timezone identifiers (e.g., "Asia/Ho_Chi_Minh", "America/New_York", "UTC+7", "GMT+7").
// It defaults to time.Local if no valid timezone is found.
func ParseLocationFromText(content string) *time.Location {
	if strings.TrimSpace(content) == "" {
		return time.Local
	}

	// 1. Check for standard IANA Area/Location patterns
	matches := ianaTzRegex.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		if len(m) > 1 {
			locName := strings.TrimRight(m[1], ".,;)")
			if loc, err := time.LoadLocation(locName); err == nil {
				return loc
			}
		}
	}

	// 2. Check for UTC/GMT offset patterns: UTC+7, UTC+07:00, GMT+7, UTC-5, etc.
	offsetMatches := offsetTzRegex.FindAllStringSubmatch(content, -1)
	for _, m := range offsetMatches {
		if len(m) >= 3 {
			sign := m[1]
			hours, err := strconv.Atoi(m[2])
			if err != nil || hours > 14 {
				continue
			}
			mins := 0
			if len(m) >= 4 && m[3] != "" {
				mins, _ = strconv.Atoi(m[3])
			}
			offsetSec := (hours*3600 + mins*60)
			if sign == "-" {
				offsetSec = -offsetSec
			}
			zoneName := fmt.Sprintf("UTC%s%d", sign, hours)
			return time.FixedZone(zoneName, offsetSec)
		}
	}

	return time.Local
}
