package zalo

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

// BotContext contains identifying metadata for the bot instance that received the update.
type BotContext struct {
	BotID       string
	BotUsername string
	BindAgent   string
}

// Router processes inbound Zalo updates, handles whitelisting, and transforms them into CanonicalMessages.
type Router struct {
	cfg     *config.Config
	hitl    *HITLCoordinator
	media   *MediaManager
	inbound chan<- domain.CanonicalMessage
}

// NewRouter creates a new inbound message router for Zalo.
func NewRouter(cfg *config.Config, hitl *HITLCoordinator, media *MediaManager, inbound chan<- domain.CanonicalMessage) *Router {
	return &Router{
		cfg:     cfg,
		hitl:    hitl,
		media:   media,
		inbound: inbound,
	}
}

// RouteUpdate handles a single inbound update.
func (r *Router) RouteUpdate(ctx context.Context, update ZaloUpdate, botCtx ...BotContext) {
	if update.Message == nil {
		return
	}
	msg := update.Message

	// Anti-Looping: Ignore updates sent by bots
	if msg.From.IsBot {
		slog.DebugContext(ctx, "ignoring update from bot user", "sender_id", msg.From.ID)
		return
	}

	// Whitelist Check: If AllowedGroupIDs is configured, ignore messages from unlisted groups
	if len(r.cfg.Zalo.AllowedGroupIDs) > 0 && msg.Chat.Type != "private" {
		allowed := false
		for _, gid := range r.cfg.Zalo.AllowedGroupIDs {
			if gid == msg.Chat.ID {
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

	// Intercept HITL Security Approval slash commands:
	// /approve <id> [session|always]
	// /deny <id>
	// /kill <id>
	if strings.HasPrefix(text, "/approve") || strings.HasPrefix(text, "/deny") || strings.HasPrefix(text, "/kill") {
		parts := strings.Fields(text)
		if len(parts) >= 2 {
			reqID := parts[1]
			action := "allow_once"
			if strings.HasPrefix(parts[0], "/deny") {
				action = "deny"
			} else if strings.HasPrefix(parts[0], "/kill") {
				action = "force_kill"
			} else if len(parts) >= 3 && (parts[2] == "session" || parts[2] == "always") {
				action = "allow_session"
			}

			if r.hitl != nil {
				err := r.hitl.HandleCommandApproval(ctx, reqID, msg.From.ID, action)
				if err != nil {
					slog.WarnContext(ctx, "failed to handle Zalo HITL approval command", "error", err, "req_id", reqID)
				}
				return
			}
		}
	}

	// Transform Inbound Attachments
	var canonicalAtts []domain.Attachment
	for _, att := range msg.Attachments {
		filePath := ""
		if r.media != nil && att.URL != "" {
			var err error
			filePath, err = r.media.DownloadInboundAttachment(ctx, att.URL, att.FileName)
			if err != nil {
				slog.WarnContext(ctx, "failed to download Zalo attachment", "url", att.URL, "error", err)
			}
		}
		canonicalAtts = append(canonicalAtts, domain.Attachment{
			ID:       att.FileID,
			FileName: att.FileName,
			FilePath: filePath,
			MIMEType: att.Type,
			Size:     att.FileSize,
			Type:     att.Type,
		})
	}

	date := time.Now()
	if msg.Date > 0 {
		date = time.Unix(msg.Date, 0)
	}

	botIDStr := ""
	botUsername := ""
	bindAgent := ""
	if len(botCtx) > 0 {
		botIDStr = botCtx[0].BotID
		botUsername = botCtx[0].BotUsername
		bindAgent = botCtx[0].BindAgent
	}

	canonical := domain.CanonicalMessage{
		ID:          msg.MessageID,
		Timestamp:   date,
		Channel:     "zalo",
		BotID:       ParseNumericID(botIDStr),
		BotUsername: botUsername,
		BindAgent:   bindAgent,
		Sender: domain.SenderUser{
			ID:       msg.From.ID,
			Username: msg.From.Username,
			FullName: msg.From.Name,
		},
		Chat: domain.ChatContext{
			ID:       msg.Chat.ID,
			Type:     msg.Chat.Type,
			Title:    msg.Chat.Title,
			ThreadID: msg.Chat.ThreadID,
		},
		Text:        msg.Text,
		RawText:     msg.Text,
		Attachments: canonicalAtts,
	}

	if msg.ReplyToMsg != nil {
		canonical.ReplyToMessageID = msg.ReplyToMsg.MessageID
	}

	select {
	case r.inbound <- canonical:
	case <-ctx.Done():
		return
	default:
		slog.WarnContext(ctx, "inbound channel full, dropping Zalo message", "message_id", msg.MessageID)
	}
}
