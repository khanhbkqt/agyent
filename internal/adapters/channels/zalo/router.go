package zalo

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

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
func (r *Router) RouteUpdate(ctx context.Context, update ZaloUpdate) {
	if update.Message == nil {
		return
	}
	msg := update.Message

	// Whitelist Check: If AllowedGroupIDs is set, ignore unlisted groups
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

	// Intercept HITL Security Approval slash commands: /approve <id> or /deny <id>
	if strings.HasPrefix(text, "/approve") || strings.HasPrefix(text, "/deny") {
		parts := strings.Fields(text)
		if len(parts) >= 2 {
			reqID := parts[1]
			isApproved := strings.HasPrefix(parts[0], "/approve")
			if r.hitl != nil {
				err := r.hitl.HandleCommandApproval(ctx, reqID, msg.From.ID, isApproved)
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

	canonical := domain.CanonicalMessage{
		ID:        msg.MessageID,
		Timestamp: date,
		Channel:   "zalo",
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
	default:
		slog.WarnContext(ctx, "inbound channel full, dropping Zalo message", "message_id", msg.MessageID)
	}
}
