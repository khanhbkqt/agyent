package domain

import "time"

// PluginManifest defines the metadata and declarations in plugin.json.
type PluginManifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      string   `json:"author,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Enabled     bool     `json:"enabled"`
	Tags        []string `json:"tags,omitempty"`
}

// Plugin represents a loaded and parsed capability bundle.
type Plugin struct {
	Manifest    PluginManifest    `json:"manifest"`
	Path        string            `json:"path"`
	Scope       ContextScope      `json:"scope"`
	MCPServers  []MCPServerConfig `json:"mcp_servers,omitempty"`
	Skills      []SkillHeader     `json:"skills,omitempty"`
	Rules       string            `json:"rules,omitempty"`
	InstalledAt time.Time         `json:"installed_at"`
}

// PluginSyncResult describes the outcome of syncing an embedded or remote plugin.
type PluginSyncResult struct {
	Name             string `json:"name"`
	InstalledVersion string `json:"installed_version"`
	EmbeddedVersion  string `json:"embedded_version"`
	Updated          bool   `json:"updated"`
	Skipped          bool   `json:"skipped"`
	Reason           string `json:"reason,omitempty"`
	Path             string `json:"path"`
}
