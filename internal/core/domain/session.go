package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Session stores the state and mapping of an interactive conversation context.
type Session struct {
	SessionKey            string    `json:"session_key"`             // e.g. "telegram:123456", "telegram:botID:123456", or "telegram:botID:-100123:42"
	ActiveAgent           string    `json:"active_agent"`            // Name of current agent (e.g. "dev_expert")
	ActiveProject         string    `json:"active_project"`          // Name of current project (or empty for Global mode)
	ActiveModel           string    `json:"active_model,omitempty"`  // Custom model override for this session
	ActiveEffort          string    `json:"active_effort,omitempty"` // Custom reasoning effort override for this session
	GlobalConversationID  string    `json:"global_conversation_id"`  // AGY conversation ID for global chat
	ProjectConversationID string    `json:"project_conversation_id"` // AGY conversation ID for active project
	UpdatedAt             time.Time `json:"updated_at"`
}

// FormatSessionKey generates a normalized namespaced session key.
// If botID > 0, format is "channel:botID:chatID[:threadID]".
// Otherwise, format falls back to legacy "channel:chatID[:threadID]".
func FormatSessionKey(channel, chatID string, threadID int64, botIDOpt ...int64) string {
	channel = strings.TrimSpace(strings.ToLower(channel))
	chatID = strings.TrimSpace(chatID)
	channel = strings.ReplaceAll(channel, ":", "_")
	chatID = strings.ReplaceAll(chatID, ":", "_")

	var botID int64
	if len(botIDOpt) > 0 {
		botID = botIDOpt[0]
	}

	if botID > 0 {
		if threadID > 0 {
			return fmt.Sprintf("%s:%d:%s:%d", channel, botID, chatID, threadID)
		}
		return fmt.Sprintf("%s:%d:%s", channel, botID, chatID)
	}

	if threadID > 0 {
		return fmt.Sprintf("%s:%s:%d", channel, chatID, threadID)
	}
	return fmt.Sprintf("%s:%s", channel, chatID)
}

// ExtractChatIDFromSessionKey accurately extracts chatID across legacy and namespaced formats.
func ExtractChatIDFromSessionKey(sessionKey string) string {
	parts := strings.Split(sessionKey, ":")
	switch len(parts) {
	case 2:
		// Legacy format: telegram:chatID
		return parts[1]
	case 3:
		// Namespaced format: telegram:botID:chatID (if parts[1] is positive botID)
		if _, err := strconv.ParseInt(parts[1], 10, 64); err == nil && !strings.HasPrefix(parts[1], "-") {
			return parts[2]
		}
		// Legacy format with group: telegram:-100123:threadID
		return parts[1]
	case 4:
		// Full namespaced format: telegram:botID:chatID:threadID
		return parts[2]
	default:
		if len(parts) > 2 {
			return parts[1]
		}
		return sessionKey
	}
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
