package domain

import (
	"strconv"
	"strings"
	"time"
)

// SenderUser represents the sender of an inbound message.
type SenderUser struct {
	ID       string `json:"id"`
	Username string `json:"username,omitempty"`
	FullName string `json:"full_name,omitempty"`
}

// ChatContext represents the context of the chat or group where a message originated.
type ChatContext struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // e.g. "private", "group", "supergroup", "channel"
	Title    string `json:"title,omitempty"`
	ThreadID int64  `json:"thread_id,omitempty"` // For forum topics / threads
}

// Attachment represents an inbound media or file attachment.
type Attachment struct {
	ID       string `json:"id"`
	FileName string `json:"file_name"`
	FilePath string `json:"file_path"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Type     string `json:"type"` // "image", "document", "audio", "video", etc.
	Caption  string `json:"caption,omitempty"`
}

// InboundAttachmentRef represents a lazy reference to an inbound media attachment.
// It avoids downloading files to disk before authorization and execution admission.
type InboundAttachmentRef struct {
	ID       string `json:"id"`
	FileName string `json:"file_name"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Type     string `json:"type"` // "image", "document", "audio", "video", etc.
	Caption  string `json:"caption,omitempty"`
	BotID    int64  `json:"bot_id,omitempty"`
	SourceID string `json:"source_id"` // Provider-specific ID (e.g. Telegram file_id)
}

// CanonicalMessage is the standardized representation of any inbound message across channels.
type CanonicalMessage struct {
	ID               string                 `json:"id"`
	Timestamp        time.Time              `json:"timestamp"`
	Channel          string                 `json:"channel"` // e.g. "telegram"
	BotID            int64                  `json:"bot_id,omitempty"`
	BotUsername      string                 `json:"bot_username,omitempty"`
	BindAgent        string                 `json:"bind_agent,omitempty"` // Dedicated bound agent for this bot
	Sender           SenderUser             `json:"sender"`
	Chat             ChatContext            `json:"chat"`
	Text             string                 `json:"text"`
	RawText          string                 `json:"raw_text"`
	Attachments      []Attachment           `json:"attachments,omitempty"`
	AttachmentRefs   []InboundAttachmentRef `json:"attachment_refs,omitempty"`
	IsMentioned      bool                   `json:"is_mentioned"`
	IsReplyToBot     bool                   `json:"is_reply_to_bot"`
	ReplyToMessageID string                 `json:"reply_to_message_id,omitempty"`
}

// OutboundAttachment represents a file or artifact to send out.
type OutboundAttachment struct {
	FilePath string `json:"file_path"`
	FileName string `json:"file_name"`
	MIMEType string `json:"mime_type"`
	Caption  string `json:"caption,omitempty"`
	Type     string `json:"type"` // "image", "document", etc.
}

// ToOutboundAttachments converts domain.Attachment slice to domain.OutboundAttachment slice.
func ToOutboundAttachments(atts []Attachment) []OutboundAttachment {
	if len(atts) == 0 {
		return nil
	}
	res := make([]OutboundAttachment, len(atts))
	for i, att := range atts {
		res[i] = OutboundAttachment{
			FilePath: att.FilePath,
			FileName: att.FileName,
			MIMEType: att.MIMEType,
			Caption:  att.Caption,
			Type:     att.Type,
		}
	}
	return res
}

// InlineButton represents an interactive button in a messaging UI.
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// InlineKeyboardRow represents a single row of buttons in an inline keyboard.
type InlineKeyboardRow []InlineButton

// InlineKeyboard represents a multi-row interactive keyboard.
type InlineKeyboard []InlineKeyboardRow

// TargetContext specifies the destination channel, bot, and chat context for message delivery.
type TargetContext struct {
	Channel   string `json:"channel"`              // e.g. "telegram", "zalo", "slack"
	BotID     string `json:"bot_id,omitempty"`     // Unique platform-agnostic bot identifier
	AgentName string `json:"agent_name,omitempty"` // Dedicated target agent persona name (e.g. "wife_assistant")
	ChatID    string `json:"chat_id"`              // Target chat / conversation ID
	ThreadID  int64  `json:"thread_id,omitempty"`  // Optional forum topic / message thread ID
}

// OutboundMessage represents a standardized response to be sent to a channel.
type OutboundMessage struct {
	Channel          string               `json:"channel,omitempty"`    // Originating channel identifier (e.g. "telegram", "zalo")
	BotID            int64                `json:"bot_id,omitempty"`     // Originating bot ID for multi-bot outbound routing
	BotIDStr         string               `json:"bot_id_str,omitempty"` // String representation of BotID
	AgentName        string               `json:"agent_name,omitempty"` // Originating or target agent persona name for dedicated bot dispatch
	ChatID           string               `json:"chat_id"`
	ThreadID         int64                `json:"thread_id,omitempty"`
	Text             string               `json:"text"`
	ParseMode        string               `json:"parse_mode,omitempty"` // "MarkdownV2", "HTML", "Markdown", ""
	Attachments      []OutboundAttachment `json:"attachments,omitempty"`
	ReplyToMessageID string               `json:"reply_to_message_id,omitempty"`
	InlineKeyboard   InlineKeyboard       `json:"inline_keyboard,omitempty"`
	WorkspaceDir     string               `json:"workspace_dir,omitempty"`
	ConversationID   string               `json:"conversation_id,omitempty"`
}

// TargetContext returns the TargetContext corresponding to this outbound message.
func (o *OutboundMessage) TargetContext() TargetContext {
	botID := o.BotIDStr
	if botID == "" && o.BotID > 0 {
		botID = strconv.FormatInt(o.BotID, 10)
	}
	return TargetContext{
		Channel:   o.Channel,
		BotID:     botID,
		AgentName: o.AgentName,
		ChatID:    o.ChatID,
		ThreadID:  o.ThreadID,
	}
}

// TargetContext returns the TargetContext for this canonical inbound message.
func (m *CanonicalMessage) TargetContext() TargetContext {
	botID := ""
	if m.BotID > 0 {
		botID = strconv.FormatInt(m.BotID, 10)
	}
	return TargetContext{
		Channel:   m.Channel,
		BotID:     botID,
		AgentName: m.BindAgent,
		ChatID:    m.Chat.ID,
		ThreadID:  m.Chat.ThreadID,
	}
}

// SessionKey returns the unique session key for this message's chat and thread context.
func (m *CanonicalMessage) SessionKey() string {
	return FormatSessionKey(m.Channel, m.Chat.ID, m.Chat.ThreadID, m.BotID)
}

// CleanText returns the trimmed text with excess whitespace normalized.
func (m *CanonicalMessage) CleanText() string {
	return strings.TrimSpace(m.Text)
}

// IsCommand checks whether the message text starts with a slash command prefix '/'.
func (m *CanonicalMessage) IsCommand() bool {
	trimmed := strings.TrimSpace(m.Text)
	return strings.HasPrefix(trimmed, "/")
}

// CommandArgs extracts the slash command and its arguments.
// For instance: "/p@my_bot hello world" -> cmd: "/p", args: ["hello", "world"]
func (m *CanonicalMessage) CommandArgs() (string, []string) {
	trimmed := strings.TrimSpace(m.Text)
	if !strings.HasPrefix(trimmed, "/") {
		return "", nil
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "", nil
	}

	rawCmd := fields[0]
	// Remove bot username suffix if present, e.g. "/p@agyent_bot" -> "/p"
	if atIdx := strings.Index(rawCmd, "@"); atIdx != -1 {
		rawCmd = rawCmd[:atIdx]
	}

	args := fields[1:]
	return rawCmd, args
}
