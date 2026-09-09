package zalo

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// BotContext contains identifying metadata for the bot instance that received the update.
type BotContext struct {
	BotID       string
	BotName     string
	BotUsername string
	BindAgent   string
}

// CleanZaloMention strips leading bot mentions from raw text,
// e.g. "@Trao Mơ FC /new" -> "/new", "@Trao Mơ FC: hello" -> "hello",
// while preserving rawText in domain.CanonicalMessage.
func CleanZaloMention(text string, botCtx BotContext) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}

	lower := strings.ToLower(trimmed)
	candidates := []string{
		botCtx.BotName,
		botCtx.BotUsername,
		botCtx.BindAgent,
	}

	for _, c := range candidates {
		c = strings.TrimSpace(strings.TrimPrefix(c, "@"))
		if c == "" {
			continue
		}
		mentionPrefix := "@" + strings.ToLower(c)
		if strings.HasPrefix(lower, mentionPrefix) {
			rest := trimmed[len(mentionPrefix):]
			rest = strings.TrimLeft(rest, ":,- \t")
			return strings.TrimSpace(rest), true
		}
	}

	// Fallback 1: If message starts with "@... /<command>", strip the mention token(s)
	// up to the slash command so that commands (e.g. /new, /reset) execute reliably
	// regardless of how many words or spaces the bot's display name contains.
	if strings.HasPrefix(trimmed, "@") {
		if parts := strings.Fields(trimmed); len(parts) >= 2 {
			for i := 1; i < len(parts); i++ {
				if strings.HasPrefix(parts[i], "/") {
					return strings.Join(parts[i:], " "), true
				}
			}
		}
		// Fallback 2: Single-token mention (e.g. "@bot hello")
		if idx := strings.Index(trimmed, " "); idx != -1 {
			token := trimmed[:idx]
			if strings.HasPrefix(token, "@") {
				rest := strings.TrimLeft(trimmed[idx:], ":,- \t")
				return strings.TrimSpace(rest), true
			}
		}
	}

	return trimmed, false
}

// Router processes authenticated Zalo updates and converts them to domain messages.
type Router struct {
	cfg        *config.Config
	hitl       *HITLCoordinator
	inbound    chan<- domain.CanonicalMessage
	authorizer ports.InboundAuthorizer
	mu         sync.RWMutex
}

// NewRouter creates a new update router. Media remains a constructor argument to
// retain source compatibility; attachment downloads are intentionally deferred to
// the core-owned attachment admission step.
func NewRouter(cfg *config.Config, hitl *HITLCoordinator, media *MediaManager, inbound chan<- domain.CanonicalMessage) *Router {
	_ = media
	return &Router{cfg: cfg, hitl: hitl, inbound: inbound}
}

// SetInboundAuthorizer injects the core admission evaluator.
func (r *Router) SetInboundAuthorizer(authorizer ports.InboundAuthorizer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authorizer = authorizer
}

// RouteUpdate handles one provider update without creating local media state
// until group filtering and core authorization have both succeeded.
func (r *Router) RouteUpdate(ctx context.Context, update ZaloUpdate, botCtx ...BotContext) {
	if update.Message == nil {
		return
	}
	msg := update.Message
	if msg.From.IsBot {
		slog.DebugContext(ctx, "ignoring update from bot user", "sender_id", msg.From.ID)
		return
	}

	if r.cfg != nil && (len(r.cfg.Zalo.AllowedGroupIDs) > 0 || r.cfg.Zalo.GroupID != "") && !msg.Chat.IsPrivate() {
		if !r.cfg.IsGroupAllowed(msg.Chat.ID, "zalo") {
			slog.DebugContext(ctx, "ignoring message from unlisted Zalo group", "chat_id", msg.Chat.ID)
			return
		}
	}

	var botCtxObj BotContext
	if len(botCtx) > 0 {
		botCtxObj = botCtx[0]
	}
	botIDStr := botCtxObj.BotID
	botUsername := botCtxObj.BotUsername
	bindAgent := botCtxObj.BindAgent
	botID := ParseNumericID(botIDStr)
	sessionKey := domain.FormatSessionKey("zalo", msg.Chat.ID, msg.Chat.ThreadID, botID)

	allAttachments := msg.CollectAttachments()

	rawText := msg.Text
	if strings.TrimSpace(rawText) == "" {
		rawText = msg.Caption
	}
	if strings.TrimSpace(rawText) == "" {
		rawText = msg.Description
	}
	if strings.TrimSpace(rawText) == "" && len(allAttachments) > 0 {
		for _, att := range allAttachments {
			if c := att.GetEffectiveCaption(); c != "" {
				rawText = c
				break
			}
		}
	}

	attachmentRefs := make([]domain.InboundAttachmentRef, 0, len(allAttachments))
	for _, attachment := range allAttachments {
		remoteURL := attachment.GetEffectiveURL()
		if remoteURL == "" {
			continue
		}
		fileID := attachment.GetEffectiveFileID()
		if fileID == "" {
			fileID = fmt.Sprintf("att_%d", time.Now().UnixNano())
		}
		attachmentType := strings.ToLower(strings.TrimSpace(attachment.Type))
		mimeType := "application/octet-stream"
		fileName := SanitizeFilename(attachment.GetEffectiveFileName())
		if attachmentType == "photo" || attachmentType == "image" || attachmentType == "" {
			attachmentType = "image"
			mimeType = "image/jpeg"
			if fileName == "file" || fileName == "" || filepath.Ext(fileName) == "" {
				fileName = fmt.Sprintf("photo_%d_%s.jpg", time.Now().Unix(), fileID)
			}
		} else if attachmentType == "voice" || attachmentType == "audio" {
			attachmentType = "audio"
			mimeType = "audio/ogg"
			if fileName == "file" || fileName == "" || filepath.Ext(fileName) == "" {
				fileName = fmt.Sprintf("audio_%d_%s.ogg", time.Now().Unix(), fileID)
			}
		} else if attachmentType == "video" {
			mimeType = "video/mp4"
			if fileName == "file" || fileName == "" || filepath.Ext(fileName) == "" {
				fileName = fmt.Sprintf("video_%d_%s.mp4", time.Now().Unix(), fileID)
			}
		} else {
			if fileName == "file" || fileName == "" {
				fileName = fmt.Sprintf("doc_%d_%s", time.Now().Unix(), fileID)
			}
		}

		caption := attachment.GetEffectiveCaption()
		if caption == "" {
			caption = msg.Caption
		}
		if caption == "" {
			caption = msg.Description
		}

		attachmentRefs = append(attachmentRefs, domain.InboundAttachmentRef{
			Channel:  "zalo",
			ID:       fileID,
			SourceID: remoteURL,
			FileName: fileName,
			MIMEType: mimeType,
			Size:     attachment.GetEffectiveFileSize(),
			Type:     attachmentType,
			Caption:  caption,
			BotID:    botID,
		})
	}

	// Zero-Empty-Prompt Guard: Ensure prompt is never empty to prevent headless TUI crashes
	if strings.TrimSpace(rawText) == "" {
		if len(attachmentRefs) > 0 {
			rawText = "[Người dùng gửi ảnh/tệp đính kèm. Em hãy kiểm tra và phân tích tệp này.]"
		} else {
			rawText = "Xin chào!"
		}
	}

	// Intercept HITL Approvals (Quote replies, /approve, /deny, /kill, short codes)
	if r.hitl != nil {
		replyToMsgID := ""
		if msg.ReplyToMsg != nil && strings.TrimSpace(msg.ReplyToMsg.MessageID) != "" {
			replyToMsgID = strings.TrimSpace(msg.ReplyToMsg.MessageID)
		}
		handled, err := r.hitl.HandleFlexibleApproval(ctx, msg.Chat.ID, replyToMsgID, rawText, msg.From.ID)
		if err != nil {
			slog.WarnContext(ctx, "error processing Zalo HITL action", "sender", msg.From.ID, "chat_id", msg.Chat.ID, "error", err)
		}
		if handled {
			return
		}
	}

	cleanText, isMentioned := CleanZaloMention(rawText, botCtxObj)
	if cleanText == "" {
		cleanText = rawText
	}

	r.mu.RLock()
	authorizer := r.authorizer
	r.mu.RUnlock()
	chatType := msg.Chat.EffectiveType()
	if authorizer != nil {
		allowed, err := authorizeInboundMessage(ctx, authorizer, msg.From.ID, bindAgent, chatType, sessionKey)
		if err != nil || !allowed {
			slog.WarnContext(ctx, "inbound Zalo message unauthorized (blocked by ACL)", "sender_id", msg.From.ID, "bind_agent", bindAgent, "chat_id", msg.Chat.ID, "chat_type", chatType, "error", err)
			return
		}
	}

	date := time.Now()
	if msg.Date > 0 {
		if msg.Date > 1_000_000_000_000 {
			date = time.UnixMilli(msg.Date)
		} else {
			date = time.Unix(msg.Date, 0)
		}
	}
	canonical := domain.CanonicalMessage{
		ID:          msg.MessageID,
		Timestamp:   date,
		Channel:     "zalo",
		BotID:       botID,
		BotUsername: botUsername,
		BindAgent:   bindAgent,
		Sender: domain.SenderUser{
			ID:       msg.From.ID,
			Provider: "zalo",
			Username: msg.From.Username,
			FullName: msg.From.GetEffectiveName(),
		},
		Chat: domain.ChatContext{
			ID:       msg.Chat.ID,
			Type:     chatType,
			Title:    msg.Chat.Title,
			ThreadID: msg.Chat.ThreadID,
		},
		Text:           cleanText,
		RawText:        rawText,
		IsMentioned:    isMentioned,
		AttachmentRefs: attachmentRefs,
	}
	if msg.ReplyToMsg != nil {
		canonical.ReplyToMessageID = msg.ReplyToMsg.MessageID

		senderName := msg.ReplyToMsg.From.GetEffectiveName()
		if senderName == "" {
			senderName = msg.ReplyToMsg.From.Username
		}
		if msg.ReplyToMsg.From.IsBot || (botCtxObj.BotID != "" && msg.ReplyToMsg.From.ID == botCtxObj.BotID) {
			canonical.IsReplyToBot = true
			if botCtxObj.BotUsername != "" {
				senderName = "@" + botCtxObj.BotUsername
			} else if senderName == "" || senderName == "Bot" {
				senderName = "Assistant"
			}
		}

		repliedText := msg.ReplyToMsg.Text
		if repliedText == "" {
			repliedText = msg.ReplyToMsg.Caption
		}
		if repliedText == "" {
			repliedText = msg.ReplyToMsg.Description
		}
		if repliedText == "" {
			if len(msg.ReplyToMsg.Photo) > 0 || msg.ReplyToMsg.Image != nil {
				repliedText = "[Photo]"
			} else if msg.ReplyToMsg.Document != nil {
				docName := msg.ReplyToMsg.Document.GetEffectiveFileName()
				if docName != "" {
					repliedText = "[Document: " + docName + "]"
				} else {
					repliedText = "[Document]"
				}
			} else if msg.ReplyToMsg.Audio != nil || msg.ReplyToMsg.Voice != nil {
				repliedText = "[Audio]"
			} else if msg.ReplyToMsg.Video != nil {
				repliedText = "[Video]"
			} else if len(msg.ReplyToMsg.Attachments) > 0 {
				repliedText = "[Attachment]"
			}
		}

		repliedText = domain.TruncateSnippet(repliedText, 300)
		if msg.ReplyToMsg.MessageID != "" || senderName != "" || repliedText != "" {
			canonical.ReplyContext = &domain.ReplyContext{
				MessageID: msg.ReplyToMsg.MessageID,
				Sender:    senderName,
				Text:      repliedText,
			}
		}
	}

	if r.inbound == nil {
		return
	}
	select {
	case r.inbound <- canonical:
	case <-ctx.Done():
	}
}

func authorizeInboundMessage(ctx context.Context, authorizer ports.InboundAuthorizer, senderID, bindAgent, chatType, sessionKey string) (bool, error) {
	if sessionAuthorizer, ok := authorizer.(ports.InboundSessionAuthorizer); ok {
		return sessionAuthorizer.AuthorizeInboundSession(ctx, senderID, bindAgent, chatType, sessionKey)
	}
	return authorizer.AuthorizeInbound(ctx, senderID, bindAgent, chatType)
}
