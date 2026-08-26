package domain

import (
	"fmt"
	"strings"
	"time"
)

// Session stores the state and mapping of an interactive conversation context.
type Session struct {
	SessionKey            string    `json:"session_key"`             // e.g. "telegram:123456" or "telegram:-100123:42"
	ActiveAgent           string    `json:"active_agent"`            // Name of current agent (e.g. "dev_expert")
	ActiveProject         string    `json:"active_project"`          // Name of current project (or empty for Global mode)
	ActiveModel           string    `json:"active_model,omitempty"`  // Custom model override for this session
	ActiveEffort          string    `json:"active_effort,omitempty"` // Custom reasoning effort override for this session
	GlobalConversationID  string    `json:"global_conversation_id"`  // AGY conversation ID for global chat
	ProjectConversationID string    `json:"project_conversation_id"` // AGY conversation ID for active project
	UpdatedAt             time.Time `json:"updated_at"`
}

// FormatSessionKey generates a normalized session key.
// If threadID is greater than 0, format is "channel:chatID:threadID".
// Otherwise, format is "channel:chatID".
func FormatSessionKey(channel, chatID string, threadID int64) string {
	channel = strings.TrimSpace(strings.ToLower(channel))
	chatID = strings.TrimSpace(chatID)
	channel = strings.ReplaceAll(channel, ":", "_")
	chatID = strings.ReplaceAll(chatID, ":", "_")
	if threadID > 0 {
		return fmt.Sprintf("%s:%s:%d", channel, chatID, threadID)
	}
	return fmt.Sprintf("%s:%s", channel, chatID)
}

// GetActiveConversationID returns the conversation ID corresponding to the current mode (Project vs Global).
func (s *Session) GetActiveConversationID() string {
	if s.ActiveProject != "" {
		return s.ProjectConversationID
	}
	return s.GlobalConversationID
}

// SetActiveConversationID updates the conversation ID corresponding to current mode.
func (s *Session) SetActiveConversationID(convID string) {
	if s.ActiveProject != "" {
		s.ProjectConversationID = convID
	} else {
		s.GlobalConversationID = convID
	}
}

// ResetActiveConversationID clears the conversation ID for the current active scope (Project or Global).
// This is used for /reset slash commands or automatic recovery when an AGY session is lost/corrupted.
func (s *Session) ResetActiveConversationID() {
	if s.ActiveProject != "" {
		s.ProjectConversationID = ""
	} else {
		s.GlobalConversationID = ""
	}
}
