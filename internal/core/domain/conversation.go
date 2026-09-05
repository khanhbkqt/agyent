package domain

import "time"

// Conversation represents a persisted conversation context catalog item.
type Conversation struct {
	ID                string    `json:"id"`                  // AGY Conversation UUID
	SessionKey        string    `json:"session_key"`         // e.g. "telegram:123456"
	AgentName         string    `json:"agent_name"`          // Associated agent name
	ProjectName       string    `json:"project_name"`        // Project name or empty for Global mode
	Title             string    `json:"title"`               // Human-friendly title
	AliasIndex        int       `json:"alias_index"`         // #1, #2, #3... for quick reference
	TurnCount         int       `json:"turn_count"`          // Number of completed turns
	IsPinned          bool      `json:"is_pinned"`           // Protected from auto-GC
	IsArchived        bool      `json:"is_archived"`         // Hidden from active list
	LastReflectedStep int       `json:"last_reflected_step"` // Last step index analyzed by evolution
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ConversationSummary is a lightweight projection for UI menus and lists.
type ConversationSummary struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	AliasIndex int       `json:"alias_index"`
	TurnCount  int       `json:"turn_count"`
	IsPinned   bool      `json:"is_pinned"`
	IsArchived bool      `json:"is_archived"`
	IsActive   bool      `json:"is_active"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ConversationScope defines the 3D boundary (SessionKey, AgentName, ProjectName) for conversation isolation.
type ConversationScope struct {
	SessionKey  string `json:"session_key"`
	AgentName   string `json:"agent_name"`
	ProjectName string `json:"project_name"`
}

// Scope extracts the ConversationScope from a Conversation.
func (c *Conversation) Scope() ConversationScope {
	if c == nil {
		return ConversationScope{}
	}
	return ConversationScope{
		SessionKey:  c.SessionKey,
		AgentName:   c.AgentName,
		ProjectName: c.ProjectName,
	}
}

// Matches checks if the scope matches the given conversation.
func (s ConversationScope) Matches(c *Conversation) bool {
	if c == nil {
		return false
	}
	return s.SessionKey == c.SessionKey && s.AgentName == c.AgentName && s.ProjectName == c.ProjectName
}
