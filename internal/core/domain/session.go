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

// ParsedSessionKey contains decoded components of a session key.
type ParsedSessionKey struct {
	Channel  string
	BotID    int64
	ChatID   string
	ThreadID int64
}

// ParseSessionKey decomposes a session key into its constituent parts across 2-part, 3-part, and 4-part formats.
// Supported formats:
// - 2-part: "channel:chatID" (e.g. "telegram:8544450322", "telegram:-100123456")
// - 3-part:
//   - "channel:botID:chatID" (e.g. "telegram:8718145628:8544450322", "telegram:8718145628:-100123456")
//   - "channel:chatID:threadID" (e.g. "telegram:-100123456:10042", "telegram:123456:0")
//
// - 4-part: "channel:botID:chatID:threadID" (e.g. "telegram:8718145628:-100123456:10042")
func ParseSessionKey(sessionKey string) (ParsedSessionKey, error) {
	parts := strings.Split(sessionKey, ":")
	if len(parts) < 2 {
		return ParsedSessionKey{}, fmt.Errorf("invalid session key format %q", sessionKey)
	}

	channel := parts[0]

	switch len(parts) {
	case 2:
		return ParsedSessionKey{
			Channel: channel,
			ChatID:  parts[1],
		}, nil

	case 3:
		// If parts[1] is a negative number, it's a legacy group chatID, and parts[2] is threadID
		if strings.HasPrefix(parts[1], "-") {
			threadID, _ := strconv.ParseInt(parts[2], 10, 64)
			return ParsedSessionKey{
				Channel:  channel,
				ChatID:   parts[1],
				ThreadID: threadID,
			}, nil
		}

		// If parts[2] is "0", it's a legacy key with explicit 0 threadID
		if parts[2] == "0" {
			return ParsedSessionKey{
				Channel: channel,
				ChatID:  parts[1],
			}, nil
		}

		// Try parsing parts[1] as botID
		if botID, err := strconv.ParseInt(parts[1], 10, 64); err == nil && botID > 0 {
			return ParsedSessionKey{
				Channel: channel,
				BotID:   botID,
				ChatID:  parts[2],
			}, nil
		}

		// Fallback: parts[1] is chatID, parts[2] is threadID
		threadID, _ := strconv.ParseInt(parts[2], 10, 64)
		return ParsedSessionKey{
			Channel:  channel,
			ChatID:   parts[1],
			ThreadID: threadID,
		}, nil

	case 4:
		botID, _ := strconv.ParseInt(parts[1], 10, 64)
		threadID, _ := strconv.ParseInt(parts[3], 10, 64)
		return ParsedSessionKey{
			Channel:  channel,
			BotID:    botID,
			ChatID:   parts[2],
			ThreadID: threadID,
		}, nil

	default:
		return ParsedSessionKey{
			Channel: channel,
			ChatID:  parts[1],
		}, nil
	}
}

// ExtractChatIDFromSessionKey accurately extracts chatID across legacy and namespaced formats.
func ExtractChatIDFromSessionKey(sessionKey string) string {
	parsed, err := ParseSessionKey(sessionKey)
	if err != nil {
		return sessionKey
	}
	return parsed.ChatID
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
