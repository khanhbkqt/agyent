package domain

import "time"

// AgentStatus defines the operational lifecycle state of an Agent.
type AgentStatus string

const (
	StatusUninitialized AgentStatus = "uninitialized"
	StatusInitialized   AgentStatus = "initialized"
)

// Agent represents an autonomous AGY persona profile and workspace.
type Agent struct {
	Name          string      `json:"name"`
	Description   string      `json:"description"`
	Status        AgentStatus `json:"status"`
	WorkspacePath string      `json:"workspace_path"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

// IsInitialized checks if the agent has finished its bootstrap protocol.
func (a *Agent) IsInitialized() bool {
	return a.Status == StatusInitialized
}
