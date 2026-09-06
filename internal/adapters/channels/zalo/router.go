package zalo

import (
	"context"
	"log/slog"
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

	if r.cfg != nil && len(r.cfg.Zalo.AllowedGroupIDs) > 0 && msg.Chat.Type != "private" {
		allowed := false
		for _, groupID := range r.cfg.Zalo.AllowedGroupIDs {
			if groupID == msg.Chat.ID {
				allowed = true
				break
			}
		}
		if !allowed {
			slog.DebugContext(ctx, "ignoring message from unlisted Zalo group", "chat_id", msg.Chat.ID)
			return
		}
	}

	text := strings.TrimSpace(msg.Text)
	if strings.HasPrefix(text, "/approve") || strings.HasPrefix(text, "/deny") || strings.HasPrefix(text, "/kill") {
		parts := strings.Fields(text)
		if len(parts) >= 2 && r.hitl != nil {
			action := "allow_once"
			switch {
			case strings.HasPrefix(parts[0], "/deny"):
				action = "deny"
			case strings.HasPrefix(parts[0], "/kill"):
				action = "force_kill"
			case len(parts) >= 3 && (parts[2] == "session" || parts[2] == "always"):
				action = "allow_session"
			}
			if err := r.hitl.HandleCommandApproval(ctx, parts[1], msg.From.ID, action); err != nil {
				slog.WarnContext(ctx, "failed to handle Zalo HITL approval command", "error", err, "request_id", parts[1])
			}
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

	cleanText, isMentioned := CleanZaloMention(msg.Text, botCtxObj)

	r.mu.RLock()
	authorizer := r.authorizer
	r.mu.RUnlock()
	if authorizer != nil {
		allowed, err := authorizeInboundMessage(ctx, authorizer, msg.From.ID, bindAgent, msg.Chat.Type, sessionKey)
		if err != nil || !allowed {
			return
		}
	}

	attachmentRefs := make([]domain.InboundAttachmentRef, 0, len(msg.Attachments))
	for _, attachment := range msg.Attachments {
		if strings.TrimSpace(attachment.URL) == "" {
			continue
		}
		attachmentType := strings.ToLower(strings.TrimSpace(attachment.Type))
		mimeType := "application/octet-stream"
		if attachmentType == "photo" || attachmentType == "image" {
			attachmentType = "image"
			mimeType = "image/jpeg"
		}
		attachmentRefs = append(attachmentRefs, domain.InboundAttachmentRef{
			Channel:  "zalo",
			ID:       attachment.FileID,
			SourceID: attachment.URL,
			FileName: attachment.FileName,
			MIMEType: mimeType,
			Size:     attachment.FileSize,
			Type:     attachmentType,
			BotID:    botID,
		})
	}

	date := time.Now()
	if msg.Date > 0 {
		date = time.Unix(msg.Date, 0)
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
			FullName: msg.From.Name,
		},
		Chat: domain.ChatContext{
			ID:       msg.Chat.ID,
			Type:     msg.Chat.Type,
			Title:    msg.Chat.Title,
			ThreadID: msg.Chat.ThreadID,
		},
		Text:           cleanText,
		RawText:        msg.Text,
		IsMentioned:    isMentioned,
		AttachmentRefs: attachmentRefs,
	}
	if msg.ReplyToMsg != nil {
		canonical.ReplyToMessageID = msg.ReplyToMsg.MessageID
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
