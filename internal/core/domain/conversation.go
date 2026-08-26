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
