package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

type pendingHITL struct {
	req       domain.ApprovalRequest
	respChan  chan domain.ApprovalDecision
	bot       *gotgbot.Bot
	botID     int64
	chatID    int64
	threadID  int64
	messageID int64
	resolved  atomic.Bool
}

// HITLCoordinator coordinates interactive approval requests over Telegram.
type HITLCoordinator struct {
	bot              *gotgbot.Bot
	botGetter        func(botID int64) *gotgbot.Bot
	botByAgentGetter func(agentName string) *gotgbot.Bot
	cfg              *config.Config
	pending          sync.Map // map[string]*pendingHITL (key = requestID)
	logger           *slog.Logger
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

// SetBotGetter configures the multi-bot resolver by botID.
func (h *HITLCoordinator) SetBotGetter(bg func(botID int64) *gotgbot.Bot) {
	h.botGetter = bg
}

// SetBotByAgentGetter configures the multi-bot resolver by agent name.
func (h *HITLCoordinator) SetBotByAgentGetter(bag func(agentName string) *gotgbot.Bot) {
	h.botByAgentGetter = bag
}

func (h *HITLCoordinator) resolveBot(botID int64, agentName string) *gotgbot.Bot {
	if h.botGetter != nil && botID > 0 {
		if b := h.botGetter(botID); b != nil {
			return b
		}
	}
	if h.botByAgentGetter != nil && agentName != "" {
		if b := h.botByAgentGetter(agentName); b != nil {
			return b
		}
	}
	return h.bot
}

// RequestApproval sends an interactive card and suspends execution until user action or timeout.
func (h *HITLCoordinator) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
	var chatID int64
	var threadID int64
	var botID int64

	parsed, err := domain.ParseSessionKey(req.SessionKey)
	if err == nil {
		botID = parsed.BotID
		threadID = parsed.ThreadID
		if c, cErr := strconv.ParseInt(parsed.ChatID, 10, 64); cErr == nil {
			chatID = c
		}
	}

	if chatID == 0 {
		// Fallback: If sessionKey cannot be parsed, check admin user IDs
		if len(h.cfg.Telegram.AdminUserIDs) > 0 {
			chatID = h.cfg.Telegram.AdminUserIDs[0]
		}
	}

	targetBot := h.resolveBot(botID, req.AgentName)

	respChan := make(chan domain.ApprovalDecision, 1)
	entry := &pendingHITL{
		req:      req,
		respChan: respChan,
		bot:      targetBot,
		botID:    botID,
		chatID:   chatID,
		threadID: threadID,
	}
	h.pending.Store(req.RequestID, entry)
	defer h.pending.Delete(req.RequestID)

	// Format interactive Telegram card
	cardText := h.formatCardText(req)
	keyboard := h.buildInlineKeyboard(req.RequestID)

	if targetBot != nil {
		opts := &gotgbot.SendMessageOpts{
			ParseMode:   "HTML",
			ReplyMarkup: keyboard,
		}
		if threadID != 0 {
			opts.MessageThreadId = threadID
		}
		formatted := FormatMarkdownToTelegramHTML(cardText)
		if utf8.RuneCountInString(formatted) > 4000 {
			formatted = FormatMarkdownToTelegramHTML(truncateString(cardText, 2500))
		}
		msg, err := targetBot.SendMessage(chatID, formatted, opts)
		if err != nil {
			h.logger.Warn("Failed to send HTML HITL approval message, retrying plain text fallback", "error", err)
			opts.ParseMode = ""
			plainText := StripHTMLTags(formatted)
			if utf8.RuneCountInString(plainText) > 4000 {
				plainText = truncateString(plainText, 3800)
			}
			msg, err = targetBot.SendMessage(chatID, plainText, opts)
			if err != nil {
				h.logger.Error("Failed to send HITL approval message after fallback", "error", err)
			}
		}
		if err == nil && msg != nil {
			entry.messageID = msg.MessageId
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
		// HandleCallbackWithBot already edited the card upon receiving the user's click.
		return dec, nil

	case <-timer.C:
		if !entry.resolved.CompareAndSwap(false, true) {
			// Callback won race right as timer expired; HandleCallbackWithBot already edited card.
			dec := <-respChan
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

// HandleCallback processes inline keyboard clicks satisfying ports.HITLApprovalPort.
func (h *HITLCoordinator) HandleCallback(ctx context.Context, callbackID string, userID int64, action string) error {
	return h.HandleCallbackWithBot(ctx, callbackID, userID, action, nil)
}

// HandleCallbackWithBot processes inline keyboard clicks with specific bot client context.
func (h *HITLCoordinator) HandleCallbackWithBot(ctx context.Context, callbackID string, userID int64, action string, bot *gotgbot.Bot) error {
	// Parse callback data: "hitl:<req_id>:<action>"
	parts := strings.Split(action, ":")
	if len(parts) < 3 || parts[0] != "hitl" {
		return fmt.Errorf("invalid hitl callback data: %s", action)
	}

	reqID := parts[1]
	act := parts[2]

	val, exists := h.pending.Load(reqID)
	if !exists {
		targetBot := h.bot
		if bot != nil {
			targetBot = bot
		}
		if targetBot != nil {
			_, _ = targetBot.AnswerCallbackQuery(callbackID, &gotgbot.AnswerCallbackQueryOpts{
				Text:      "⏱️ Yêu cầu này đã hết hạn hoặc đã được xử lý.",
				ShowAlert: true,
			})
		}
		return nil
	}
	entry := val.(*pendingHITL)

	targetBot := bot
	if targetBot == nil && entry.bot != nil {
		targetBot = entry.bot
	}
	if targetBot == nil {
		targetBot = h.resolveBot(entry.botID, entry.req.AgentName)
	}

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
		if targetBot != nil {
			_, _ = targetBot.AnswerCallbackQuery(callbackID, &gotgbot.AnswerCallbackQueryOpts{
				Text:      "⛔ You are not authorized to approve this security request!",
				ShowAlert: true,
			})
		}
		return nil
	}

	if !entry.resolved.CompareAndSwap(false, true) {
		if targetBot != nil {
			_, _ = targetBot.AnswerCallbackQuery(callbackID, &gotgbot.AnswerCallbackQueryOpts{
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

	if targetBot != nil {
		toast := "✅ Action approved."
		if !approved {
			toast = "❌ Action denied."
		}
		_, _ = targetBot.AnswerCallbackQuery(callbackID, &gotgbot.AnswerCallbackQueryOpts{
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
	if req.AgentName != "" {
		sb.WriteString(fmt.Sprintf("🤖 **Agent**      : `%s`\n", req.AgentName))
	}
	sb.WriteString(fmt.Sprintf("🛠️ **Tool**       : `%s`\n", req.ToolName))
	if req.CommandLine != "" {
		cmd := truncateString(req.CommandLine, 800)
		if strings.Contains(cmd, "\n") || len(cmd) > 100 || strings.Contains(cmd, "`") {
			sb.WriteString(fmt.Sprintf("⚙️ **Command**    :\n```bash\n%s\n```\n", cmd))
		} else {
			sb.WriteString(fmt.Sprintf("⚙️ **Command**    : `%s`\n", cmd))
		}
	}
	if req.TargetFile != "" {
		target := truncateString(req.TargetFile, 250)
		sb.WriteString(fmt.Sprintf("📂 **Target File**: `%s`\n", target))
	}
	if req.Reason != "" {
		reason := truncateString(req.Reason, 300)
		sb.WriteString(fmt.Sprintf("⚠️ **Reason**     : %s\n", reason))
	}
	if req.DiffPreview != "" {
		diff := truncateString(req.DiffPreview, 1200)
		if strings.Contains(diff, "\n") || len(diff) > 120 {
			sb.WriteString(fmt.Sprintf("📝 **Diff / Changes**:\n```\n%s\n```\n", diff))
		} else {
			sb.WriteString(fmt.Sprintf("📝 **Diff / Changes**: %s\n", diff))
		}
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
	targetBot := entry.bot
	if targetBot == nil {
		targetBot = h.resolveBot(entry.botID, entry.req.AgentName)
	}
	if targetBot == nil || entry.messageID == 0 {
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
	if utf8.RuneCountInString(formatted) > 4000 {
		formatted = FormatMarkdownToTelegramHTML(truncateString(updated, 2500))
	}

	opts := &gotgbot.EditMessageTextOpts{
		ChatId:    entry.chatID,
		MessageId: entry.messageID,
		ParseMode: "HTML",
		Text:      formatted,
	}

	_, _, err := targetBot.EditMessageText(opts)
	if err != nil {
		h.logger.Warn("Failed to edit HITL card HTML, attempting plaintext fallback", "error", err)
		opts.ParseMode = ""
		plainText := StripHTMLTags(formatted)
		if utf8.RuneCountInString(plainText) > 4000 {
			plainText = truncateString(plainText, 3800)
		}
		opts.Text = plainText
		_, _, _ = targetBot.EditMessageText(opts)
	}
}

func truncateString(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	total := len(runes)
	if total <= maxRunes {
		return s
	}
	omitted := total - maxRunes
	var suffix string
	if strings.Contains(s, "\n") {
		suffix = fmt.Sprintf("\n... [%d chars omitted]", omitted)
	} else {
		suffix = fmt.Sprintf("... [%d chars omitted]", omitted)
	}
	suffixRunes := utf8.RuneCountInString(suffix)
	if maxRunes > suffixRunes {
		return string(runes[:maxRunes-suffixRunes]) + suffix
	}
	return string(runes[:maxRunes])
}
