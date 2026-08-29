package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

type pendingHITL struct {
	req       domain.ApprovalRequest
	respChan  chan domain.ApprovalDecision
	chatID    int64
	threadID  int64
	messageID int64
	resolved  atomic.Bool
}

// HITLCoordinator coordinates interactive approval requests over Telegram.
type HITLCoordinator struct {
	bot     *gotgbot.Bot
	cfg     *config.Config
	pending sync.Map // map[string]*pendingHITL (key = requestID)
	logger  *slog.Logger
}

// NewHITLCoordinator constructs a new Telegram HITL approval coordinator.
func NewHITLCoordinator(bot *gotgbot.Bot, cfg *config.Config, logger *slog.Logger) *HITLCoordinator {
	if logger == nil {
		logger = slog.Default()
	}
	return &HITLCoordinator{
		bot:    bot,
		cfg:    cfg,
		logger: logger,
	}
}

// SetBot assigns or updates the active Telegram bot instance.
func (h *HITLCoordinator) SetBot(bot *gotgbot.Bot) {
	h.bot = bot
}

// RequestApproval sends an interactive card and suspends execution until user action or timeout.
func (h *HITLCoordinator) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
	_, chatID, threadID, err := ParseSessionKey(req.SessionKey)
	if err != nil {
		// Fallback: If sessionKey cannot be parsed, check admin user IDs
		if len(h.cfg.Telegram.AdminUserIDs) > 0 {
			chatID = h.cfg.Telegram.AdminUserIDs[0]
		}
	}

	respChan := make(chan domain.ApprovalDecision, 1)
	entry := &pendingHITL{
		req:      req,
		respChan: respChan,
		chatID:   chatID,
		threadID: threadID,
	}
	h.pending.Store(req.RequestID, entry)
	defer h.pending.Delete(req.RequestID)

	// Format interactive Telegram card
	cardText := h.formatCardText(req)
	keyboard := h.buildInlineKeyboard(req.RequestID)

	if h.bot != nil {
		opts := &gotgbot.SendMessageOpts{
			ParseMode:   "HTML",
			ReplyMarkup: keyboard,
		}
		if threadID != 0 {
			opts.MessageThreadId = threadID
		}
		formatted := FormatMarkdownToTelegramHTML(cardText)
		msg, err := h.bot.SendMessage(chatID, formatted, opts)
		if err == nil && msg != nil {
			entry.messageID = msg.MessageId
		} else {
			h.logger.Warn("Failed to send HITL approval message", "error", err)
		}
	}

	timeout := time.Until(req.ExpiresAt)
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case dec := <-respChan:
		h.updateCardOnDecision(entry, dec)
		return dec, nil

	case <-timer.C:
		if !entry.resolved.CompareAndSwap(false, true) {
			// Callback won race right as timer expired
			dec := <-respChan
			h.updateCardOnDecision(entry, dec)
			return dec, nil
		}
		dec := domain.ApprovalDecision{
			RequestID: req.RequestID,
			Action:    "timeout",
			Approved:  false,
			Timestamp: time.Now(),
		}
		h.updateCardOnDecision(entry, dec)
		return dec, nil

	case <-ctx.Done():
		if !entry.resolved.CompareAndSwap(false, true) {
			dec := <-respChan
			h.updateCardOnDecision(entry, dec)
			return dec, nil
		}
		dec := domain.ApprovalDecision{
			RequestID: req.RequestID,
			Action:    "cancelled",
			Approved:  false,
			Timestamp: time.Now(),
		}
		h.updateCardOnDecision(entry, dec)
		return dec, ctx.Err()
	}
}

// HandleCallback processes inline keyboard clicks with strict Admin RBAC verification.
func (h *HITLCoordinator) HandleCallback(ctx context.Context, callbackID string, userID int64, action string) error {
	// Parse callback data: "hitl:<req_id>:<action>"
	parts := strings.Split(action, ":")
	if len(parts) < 3 || parts[0] != "hitl" {
		return fmt.Errorf("invalid hitl callback data: %s", action)
	}

	reqID := parts[1]
	act := parts[2]

	val, exists := h.pending.Load(reqID)
	if !exists {
		if h.bot != nil {
			_, _ = h.bot.AnswerCallbackQuery(callbackID, &gotgbot.AnswerCallbackQueryOpts{
				Text:      "⏱️ Yêu cầu này đã hết hạn hoặc đã được xử lý.",
				ShowAlert: true,
			})
		}
		return nil
	}
	entry := val.(*pendingHITL)

	// RBAC Check: Ensure user is admin
	isAdmin := false
	for _, adminID := range h.cfg.Telegram.AdminUserIDs {
		if adminID == userID {
			isAdmin = true
			break
		}
	}
	if !isAdmin {
		for _, adminID := range h.cfg.Security.AdminUserIDs {
			if adminID == userID {
				isAdmin = true
				break
			}
		}
	}

	if !isAdmin {
		if h.bot != nil {
			_, _ = h.bot.AnswerCallbackQuery(callbackID, &gotgbot.AnswerCallbackQueryOpts{
				Text:      "⛔ You are not authorized to approve this security request!",
				ShowAlert: true,
			})
		}
		return nil
	}

	if !entry.resolved.CompareAndSwap(false, true) {
		if h.bot != nil {
			_, _ = h.bot.AnswerCallbackQuery(callbackID, &gotgbot.AnswerCallbackQueryOpts{
				Text:      "⏱️ This approval request has already been processed or expired.",
				ShowAlert: true,
			})
		}
		return nil
	}

	approved := act == "allow_once" || act == "allow_session"
	decision := domain.ApprovalDecision{
		RequestID: reqID,
		UserID:    userID,
		Action:    act,
		Approved:  approved,
		Timestamp: time.Now(),
	}

	if h.bot != nil {
		toast := "✅ Action approved."
		if !approved {
			toast = "❌ Action denied."
		}
		_, _ = h.bot.AnswerCallbackQuery(callbackID, &gotgbot.AnswerCallbackQueryOpts{
			Text: toast,
		})
	}

	select {
	case entry.respChan <- decision:
	default:
	}
	h.updateCardOnDecision(entry, decision)
	return nil
}

// CancelPendingRequest terminates a pending approval request.
func (h *HITLCoordinator) CancelPendingRequest(requestID string) {
	if val, exists := h.pending.Load(requestID); exists {
		entry := val.(*pendingHITL)
		if entry.resolved.CompareAndSwap(false, true) {
			select {
			case entry.respChan <- domain.ApprovalDecision{
				RequestID: requestID,
				Action:    "cancelled",
				Approved:  false,
				Timestamp: time.Now(),
			}:
			default:
			}
		}
	}
}

// CancelPendingRequestsForSession terminates all active approval cards for a given session.
func (h *HITLCoordinator) CancelPendingRequestsForSession(sessionKey string) {
	h.pending.Range(func(key, val interface{}) bool {
		entry, ok := val.(*pendingHITL)
		if ok && entry.req.SessionKey == sessionKey {
			if entry.resolved.CompareAndSwap(false, true) {
				select {
				case entry.respChan <- domain.ApprovalDecision{
					RequestID: entry.req.RequestID,
					Action:    "cancelled",
					Approved:  false,
					Timestamp: time.Now(),
				}:
				default:
				}
				h.updateCardOnDecision(entry, domain.ApprovalDecision{
					RequestID: entry.req.RequestID,
					Action:    "cancelled",
					Approved:  false,
					Timestamp: time.Now(),
				})
			}
		}
		return true
	})
}

func (h *HITLCoordinator) formatCardText(req domain.ApprovalRequest) string {
	var sb strings.Builder
	sb.WriteString("🛡️ **[Agyent Security Gateway] Approval Request**\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("🛠️ **Tool**       : `%s`\n", req.ToolName))
	if req.CommandLine != "" {
		sb.WriteString(fmt.Sprintf("⚙️ **Command**    : `%s`\n", req.CommandLine))
	}
	if req.TargetFile != "" {
		sb.WriteString(fmt.Sprintf("📂 **Target File**: `%s`\n", req.TargetFile))
	}
	if req.DiffPreview != "" {
		sb.WriteString(fmt.Sprintf("🔍 **Reason/Diff**: %s\n", req.DiffPreview))
	}
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("⚠️ *This sensitive action requires Administrator approval before execution.*")
	return sb.String()
}

func (h *HITLCoordinator) buildInlineKeyboard(reqID string) gotgbot.InlineKeyboardMarkup {
	return gotgbot.InlineKeyboardMarkup{
		InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
			{
				{
					Text:         "✅ Allow Once",
					CallbackData: fmt.Sprintf("hitl:%s:allow_once", reqID),
				},
				{
					Text:         "🛡️ Allow for Session",
					CallbackData: fmt.Sprintf("hitl:%s:allow_session", reqID),
				},
			},
			{
				{
					Text:         "❌ Deny",
					CallbackData: fmt.Sprintf("hitl:%s:deny", reqID),
				},
				{
					Text:         "🛑 Force Kill Agent",
					CallbackData: fmt.Sprintf("hitl:%s:force_kill", reqID),
				},
			},
		},
	}
}

func (h *HITLCoordinator) updateCardOnDecision(entry *pendingHITL, dec domain.ApprovalDecision) {
	if h.bot == nil || entry.messageID == 0 {
		return
	}

	var statusText string
	switch dec.Action {
	case "allow_once":
		statusText = "✅ **APPROVED (ONE-TIME)**"
	case "allow_session":
		statusText = "🛡️ **APPROVED FOR ENTIRE SESSION**"
	case "deny":
		statusText = "❌ **DENIED BY ADMINISTRATOR**"
	case "force_kill":
		statusText = "🛑 **AGENT FORCIBLY TERMINATED**"
	case "timeout":
		statusText = "⏱️ **APPROVAL TIMED OUT (AUTO-DENIED)**"
	default:
		statusText = "🚫 **REQUEST CANCELLED**"
	}

	updated := fmt.Sprintf("%s\n\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n📌 **Status:** %s", h.formatCardText(entry.req), statusText)
	formatted := FormatMarkdownToTelegramHTML(updated)
	_, _, _ = h.bot.EditMessageText(&gotgbot.EditMessageTextOpts{
		ChatId:    entry.chatID,
		MessageId: entry.messageID,
		ParseMode: "HTML",
		Text:      formatted,
	})
}
