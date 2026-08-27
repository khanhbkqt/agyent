package domain

import "time"

// AgentStatus defines the operational lifecycle state of an Agent.
type AgentStatus string

const (
	StatusUninitialized AgentStatus = "uninitialized"
	StatusInitialized   AgentStatus = "initialized"
)

// Agent represents an autonomous AGY persona profile, workspace, and ownership boundary.
type Agent struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Status         AgentStatus    `json:"status"`
	WorkspacePath  string         `json:"workspace_path"`
	DefaultModel   string         `json:"default_model,omitempty"`   // Preferred model for this agent persona
	DefaultEffort  string         `json:"default_effort,omitempty"`  // Preferred reasoning effort for this agent persona
	SecurityPreset SecurityPreset `json:"security_preset,omitempty"` // Baseline / active security preset (e.g. balanced, strict)

	// Ownership & Access Control
	OwnerID  string `json:"owner_id"`  // Telegram User ID of the creator
	IsPublic bool   `json:"is_public"` // true: Accessible by all; false: Owner + Shared members only

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AgentPermission represents access grants for collaborator users.
type AgentPermission struct {
	AgentName string    `json:"agent_name"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"` // "admin", "operator", "viewer"
	GrantedBy string    `json:"granted_by"`
	GrantedAt time.Time `json:"granted_at"`
}

// IsInitialized checks if the agent has finished its bootstrap protocol.
func (a *Agent) IsInitialized() bool {
	return a.Status == StatusInitialized
}
