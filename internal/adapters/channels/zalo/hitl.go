package zalo

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.HITLApprovalPort = (*HITLCoordinator)(nil)

type pendingHITL struct {
	req        domain.ApprovalRequest
	respChan   chan domain.ApprovalDecision
	resolved   atomic.Bool
	cardChatID string
	cardMsgID  string
	createdAt  time.Time
}

// HITLCoordinator coordinates Human-in-the-Loop approval requests over Zalo.
type HITLCoordinator struct {
	client             *Client
	cfg                *config.Config
	mu                 sync.RWMutex
	pendingByID        map[string]*pendingHITL // full RequestID -> entry
	pendingByShortCode map[string]*pendingHITL // 4-digit ShortCode -> entry
	pendingByMessageID map[string]*pendingHITL // Zalo sent message ID -> entry
	latestByChatID     map[string]*pendingHITL // chatID -> most recent entry
	shortCodeCounter   uint32
}

// NewHITLCoordinator initializes HITL approval management for Zalo.
func NewHITLCoordinator(client *Client, cfg *config.Config) *HITLCoordinator {
	return &HITLCoordinator{
		client:             client,
		cfg:                cfg,
		pendingByID:        make(map[string]*pendingHITL),
		pendingByShortCode: make(map[string]*pendingHITL),
		pendingByMessageID: make(map[string]*pendingHITL),
		latestByChatID:     make(map[string]*pendingHITL),
	}
}

// SetClient updates the active Zalo API client for HITL notifications.
func (h *HITLCoordinator) SetClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.client = client
}

func (h *HITLCoordinator) registerPending(entry *pendingHITL) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.pendingByID[entry.req.RequestID] = entry
	if entry.req.ShortCode != "" {
		h.pendingByShortCode[entry.req.ShortCode] = entry
	}
	if entry.cardChatID != "" {
		h.latestByChatID[entry.cardChatID] = entry
	}
	if entry.cardMsgID != "" {
		h.pendingByMessageID[entry.cardMsgID] = entry
	}
}

func (h *HITLCoordinator) updateMessageID(reqID, msgID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if entry, ok := h.pendingByID[reqID]; ok {
		entry.cardMsgID = msgID
		if msgID != "" {
			h.pendingByMessageID[msgID] = entry
		}
	}
}

func (h *HITLCoordinator) removePending(reqID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if entry, ok := h.pendingByID[reqID]; ok {
		delete(h.pendingByID, reqID)
		if entry.req.ShortCode != "" && h.pendingByShortCode[entry.req.ShortCode] == entry {
			delete(h.pendingByShortCode, entry.req.ShortCode)
		}
		if entry.cardMsgID != "" && h.pendingByMessageID[entry.cardMsgID] == entry {
			delete(h.pendingByMessageID, entry.cardMsgID)
		}
		if h.latestByChatID[entry.cardChatID] == entry {
			delete(h.latestByChatID, entry.cardChatID)
		}
	}
}

// RequestApproval sends an interactive card and suspends execution until user action or timeout.
func (h *HITLCoordinator) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
	chatID := domain.ExtractChatIDFromSessionKey(req.SessionKey)
	if chatID == "" && h.cfg != nil {
		chatID = h.cfg.Zalo.GroupID
	}

	if req.ShortCode == "" {
		seq := atomic.AddUint32(&h.shortCodeCounter, 1)
		req.ShortCode = fmt.Sprintf("%04d", 1000+(seq%9000))
	}

	cardText := h.formatApprovalCard(req)

	respChan := make(chan domain.ApprovalDecision, 1)
	entry := &pendingHITL{
		req:        req,
		respChan:   respChan,
		cardChatID: chatID,
		createdAt:  time.Now(),
	}

	h.registerPending(entry)
	defer h.removePending(req.RequestID)

	h.mu.RLock()
	client := h.client
	h.mu.RUnlock()

	if client != nil && chatID != "" {
		sentMsg, sendErr := client.SendMessage(ctx, SendMessageRequest{
			ChatID:    chatID,
			Text:      cardText,
			ParseMode: "markdown",
		})
		if sendErr != nil {
			slog.ErrorContext(ctx, "failed to send Zalo HITL approval card", "error", sendErr, "req_id", req.RequestID)
		} else if sentMsg != nil && sentMsg.MessageID != "" {
			h.updateMessageID(req.RequestID, sentMsg.MessageID)
		}
	}

	timeout := req.ExpiresAt.Sub(time.Now())
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case decision := <-respChan:
		return decision, nil

	case <-timer.C:
		if entry.resolved.CompareAndSwap(false, true) {
			h.notifyDecisionTimeout(chatID, req.RequestID)
			return domain.ApprovalDecision{
				RequestID: req.RequestID,
				Action:    domain.ActionTimeout,
				Approved:  false,
				Timestamp: time.Now(),
			}, nil
		}
		// Concurrently resolved by user right as timer fired
		select {
		case decision := <-respChan:
			return decision, nil
		default:
		}

	case <-ctx.Done():
		if entry.resolved.CompareAndSwap(false, true) {
			return domain.ApprovalDecision{
				RequestID: req.RequestID,
				Action:    domain.ActionCancelled,
				Approved:  false,
				Timestamp: time.Now(),
			}, ctx.Err()
		}
		// Concurrently resolved by user right as context cancelled
		select {
		case decision := <-respChan:
			return decision, nil
		default:
		}
	}

	return domain.ApprovalDecision{
		RequestID: req.RequestID,
		Action:    domain.ActionDeny,
		Approved:  false,
		Timestamp: time.Now(),
	}, nil
}

// HandleFlexibleApproval processes flexible user approvals over Zalo:
// - Quote/Reply to approval card message with keywords ('1', 'ok', 'all', 'deny', etc.)
// - Quick commands without ID: /approve, /approve session, /approve all, /deny
// - Commands with short code: /approve 4821 [all|session], /deny 4821, #4821 all
// Returns true if the message was recognized and consumed as an approval interaction.
func (h *HITLCoordinator) HandleFlexibleApproval(ctx context.Context, chatID, replyToMsgID, rawText, userID string) (bool, error) {
	text := strings.TrimSpace(rawText)
	if text == "" {
		return false, nil
	}

	var target *pendingHITL
	var rawAction string

	h.mu.RLock()
	// 1. Resolve by quoted card MessageID
	if replyToMsgID != "" {
		target = h.pendingByMessageID[replyToMsgID]
	}
	h.mu.RUnlock()

	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])

	isSlashApprove := cmd == "/approve" || strings.HasPrefix(cmd, "/approve@")
	isSlashDeny := cmd == "/deny" || strings.HasPrefix(cmd, "/deny@")
	isSlashKill := cmd == "/kill" || strings.HasPrefix(cmd, "/kill@")
	isShortCodeDirect := len(cmd) == 5 && strings.HasPrefix(cmd, "#") && isAllDigits(cmd[1:])

	// If quoting the approval card directly
	if target != nil {
		if isSlashDeny {
			rawAction = string(domain.ActionDeny)
		} else if isSlashKill {
			rawAction = string(domain.ActionForceKill)
		} else if isSlashApprove {
			if len(parts) >= 2 {
				rawAction = strings.Join(parts[1:], " ")
			} else {
				rawAction = string(domain.ActionAllowOnce)
			}
		} else {
			rawAction = text
		}
	} else if isSlashApprove || isSlashDeny || isSlashKill || isShortCodeDirect {
		// Parse explicit ID, short code, or fallback to latest in chat
		h.mu.RLock()
		if isShortCodeDirect {
			// e.g. #4821 all or #4821
			code := strings.TrimPrefix(cmd, "#")
			target = h.pendingByShortCode[code]
			if len(parts) >= 2 {
				rawAction = strings.Join(parts[1:], " ")
			} else {
				rawAction = string(domain.ActionAllowOnce)
			}
			h.mu.RUnlock()
			if target == nil {
				// Normal conversation message that happens to match #\d{4}; do not hijack
				return false, nil
			}
		} else {
			// /approve, /deny, /kill
			var unknownTargetToken string
			if len(parts) >= 2 {
				token := strings.TrimPrefix(parts[1], "#")
				// Check if parts[1] is an ID or short code
				if entry, ok := h.pendingByShortCode[token]; ok {
					target = entry
					if len(parts) >= 3 {
						rawAction = strings.Join(parts[2:], " ")
					}
				} else if entry, ok := h.pendingByID[token]; ok {
					target = entry
					if len(parts) >= 3 {
						rawAction = strings.Join(parts[2:], " ")
					}
				} else {
					// Check if remaining words form an action modifier (e.g. /approve all, /approve session, /approve tất cả)
					candidateAction := strings.Join(parts[1:], " ")
					if _, isAction := domain.ParseApprovalAction(candidateAction); isAction {
						target = h.latestByChatID[chatID]
						rawAction = candidateAction
					} else {
						// parts[1] was an explicit code or ID that was not found; do NOT touch latestByChatID!
						unknownTargetToken = parts[1]
					}
				}
			} else {
				// Bare /approve, /deny, /kill -> binds to latest pending in chat
				target = h.latestByChatID[chatID]
			}

			if isSlashDeny {
				rawAction = string(domain.ActionDeny)
			} else if isSlashKill {
				rawAction = string(domain.ActionForceKill)
			} else if isSlashApprove && rawAction == "" {
				rawAction = string(domain.ActionAllowOnce)
			}
			h.mu.RUnlock()

			if unknownTargetToken != "" {
				h.sendMessageToChat(chatID, fmt.Sprintf("⚠️ Không tìm thấy yêu cầu phê duyệt với mã hoặc ID `%s`.", unknownTargetToken))
				return true, nil
			}

			if target == nil {
				h.sendMessageToChat(chatID, "⚠️ Không tìm thấy yêu cầu phê duyệt nào đang chờ xử lý trong đoạn chat này hoặc yêu cầu đã hết hạn.")
				return true, nil
			}
		}
	} else {
		// Not an approval card quote and not a recognized approval command
		return false, nil
	}

	// RBAC Check: Fail-closed verification
	if !h.isUserAdmin(userID) {
		h.sendMessageToChat(chatID, fmt.Sprintf("⛔ Quản trị viên: Người dùng `%s` không có quyền phê duyệt yêu cầu bảo mật này.", userID))
		return true, nil
	}

	canonicalAct, ok := domain.ParseApprovalAction(rawAction)
	if !ok {
		// If user typed something invalid while quoting
		h.sendMessageToChat(chatID, "⚠️ Không nhận diện được hành động phê duyệt. Vui lòng phản hồi: `1`/`ok` (1 lần), `2`/`session` (cả phiên), `3`/`all` (tất cả cả phiên) hoặc `4`/`deny` (từ chối).")
		return true, nil
	}

	if !target.resolved.CompareAndSwap(false, true) {
		h.sendMessageToChat(chatID, "⏱️ Yêu cầu phê duyệt này đã được xử lý trước đó hoặc đã hết hạn.")
		return true, nil
	}

	approved := canonicalAct == domain.ActionAllowOnce || canonicalAct == domain.ActionAllowSession || canonicalAct == domain.ActionAllowAllSession
	decision := domain.ApprovalDecision{
		RequestID: target.req.RequestID,
		UserID:    parseUserID(userID),
		Action:    canonicalAct,
		Approved:  approved,
		Timestamp: time.Now(),
	}

	select {
	case target.respChan <- decision:
	default:
	}

	h.notifyDecisionResult(target.cardChatID, target.req.RequestID, string(canonicalAct), approved, userID)
	return true, nil
}

// HandleCommandApproval processes text commands (/approve <req_id> [session], /deny <req_id>, /kill <req_id>).
func (h *HITLCoordinator) HandleCommandApproval(ctx context.Context, reqID, userID, action string) error {
	var target *pendingHITL

	h.mu.RLock()
	cleanID := strings.TrimPrefix(reqID, "#")
	if entry, ok := h.pendingByID[cleanID]; ok {
		target = entry
	} else if entry, ok := h.pendingByShortCode[cleanID]; ok {
		target = entry
	}
	h.mu.RUnlock()

	if target == nil {
		return fmt.Errorf("%w: yêu cầu phê duyệt `%s` không tồn tại hoặc đã hết hạn", ports.ErrNotFound, reqID)
	}

	// RBAC Check
	if !h.isUserAdmin(userID) {
		return fmt.Errorf("%w: bạn không có quyền quản trị để phê duyệt yêu cầu này", ports.ErrAccessDenied)
	}

	if !target.resolved.CompareAndSwap(false, true) {
		return fmt.Errorf("yêu cầu này đã được xử lý trước đó")
	}

	canonicalAct, ok := domain.ParseApprovalAction(action)
	if !ok {
		canonicalAct = domain.ActionDeny
	}

	approved := canonicalAct == domain.ActionAllowOnce || canonicalAct == domain.ActionAllowSession || canonicalAct == domain.ActionAllowAllSession

	decision := domain.ApprovalDecision{
		RequestID: target.req.RequestID,
		UserID:    parseUserID(userID),
		Action:    canonicalAct,
		Approved:  approved,
		Timestamp: time.Now(),
	}

	select {
	case target.respChan <- decision:
	default:
	}

	h.notifyDecisionResult(target.cardChatID, target.req.RequestID, string(canonicalAct), approved, userID)
	return nil
}

// HandleCallback satisfies ports.HITLApprovalPort interface.
func (h *HITLCoordinator) HandleCallback(ctx context.Context, callbackID string, userID int64, action string) error {
	parts := strings.Split(action, ":")
	if len(parts) < 3 || parts[0] != "hitl" {
		return fmt.Errorf("invalid hitl callback data: %s", action)
	}
	reqID := parts[1]
	act := parts[2]
	return h.HandleCommandApproval(ctx, reqID, fmt.Sprintf("%d", userID), act)
}

// CancelPendingRequest terminates a pending approval request.
func (h *HITLCoordinator) CancelPendingRequest(requestID string) {
	h.mu.RLock()
	entry, exists := h.pendingByID[requestID]
	h.mu.RUnlock()

	if exists && entry.resolved.CompareAndSwap(false, true) {
		select {
		case entry.respChan <- domain.ApprovalDecision{
			RequestID: requestID,
			Action:    domain.ActionCancelled,
			Approved:  false,
			Timestamp: time.Now(),
		}:
		default:
		}
	}
}

// CancelPendingRequestsForSession terminates all pending approval requests for a given session.
func (h *HITLCoordinator) CancelPendingRequestsForSession(sessionKey string) {
	h.mu.RLock()
	var toCancel []string
	for reqID, entry := range h.pendingByID {
		if entry.req.SessionKey == sessionKey {
			toCancel = append(toCancel, reqID)
		}
	}
	h.mu.RUnlock()

	for _, reqID := range toCancel {
		h.CancelPendingRequest(reqID)
	}
}

func (h *HITLCoordinator) formatApprovalCard(req domain.ApprovalRequest) string {
	f := NewFormatter()
	f.Heading("🛡️ YÊU CẦU PHÊ DUYỆT BẢO MẬT (HITL)", 1)
	if req.ShortCode != "" {
		f.Bold(fmt.Sprintf("Mã duyệt nhanh: #%s", req.ShortCode)).NewLine()
	}
	f.ListItem(fmt.Sprintf("Mã yêu cầu (ID): `%s`", req.RequestID))
	if req.AgentName != "" {
		f.ListItem(fmt.Sprintf("Tác nhân (Agent): **%s**", req.AgentName))
	}
	f.ListItem(fmt.Sprintf("Công cụ (Tool): **%s**", req.ToolName))
	if req.CommandLine != "" {
		if strings.Contains(req.CommandLine, "\n") || len(req.CommandLine) > 80 {
			f.Text("• Lệnh thực thi:\n").CodeBlock(req.CommandLine, "bash")
		} else {
			f.ListItem(fmt.Sprintf("Lệnh thực thi: `%s`", req.CommandLine))
		}
	}
	if req.TargetFile != "" {
		f.ListItem(fmt.Sprintf("Tập tin: `%s`", req.TargetFile))
	}
	if req.Reason != "" {
		f.ListItem(fmt.Sprintf("Lý do: %s", req.Reason))
	}
	if req.DiffPreview != "" {
		f.Text("• Xem trước thay đổi (Diff):\n").CodeBlock(req.DiffPreview, "diff")
	}
	if req.RiskLevel != "" {
		f.ListItem(fmt.Sprintf("Mức độ rủi ro: **%s**", req.RiskLevel))
	}
	f.NewLine()
	f.Divider()
	f.Bold("👉 3 CÁCH PHÊ DUYỆT (Dành cho Admin):").NewLine()
	f.Bold("1️⃣ Trả lời (Quote) tin nhắn này kèm số/từ khóa:").NewLine()
	f.Text("   • `1` hoặc `ok` : Cho phép 1 lần\n")
	f.Text("   • `2` hoặc `session` : Cho phép lệnh này cả phiên\n")
	f.Text("   • `3` hoặc `all` : Cho phép tất cả cả phiên\n")
	f.Text("   • `4` hoặc `deny` : Từ chối\n")
	f.Bold("2️⃣ Lệnh nhanh trong chat (tự động nhận diện):").NewLine()
	f.Text("   • `/approve` (1 lần)\n")
	f.Text("   • `/approve session` (lệnh này cả phiên)\n")
	f.Text("   • `/approve all` (tất cả cả phiên)\n")
	f.Text("   • `/deny` (từ chối)\n")
	if req.ShortCode != "" {
		f.Bold(fmt.Sprintf("3️⃣ Lệnh kèm mã #%s:", req.ShortCode)).NewLine()
		f.Text(fmt.Sprintf("   • `/approve %s [all|session]`\n", req.ShortCode))
		f.Text(fmt.Sprintf("   • `/deny %s`\n", req.ShortCode))
	}
	f.NewLine()
	f.Italic("⏱️ Yêu cầu sẽ tự động hết hạn và hủy nếu không phản hồi trong 60 giây.")
	return f.BuildMarkdown()
}

func (h *HITLCoordinator) sendMessageToChat(chatID, text string) {
	h.mu.RLock()
	client := h.client
	h.mu.RUnlock()

	if client == nil || chatID == "" {
		return
	}
	_, _ = client.SendMessage(context.Background(), SendMessageRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "markdown",
	})
}

func (h *HITLCoordinator) notifyDecisionResult(chatID, reqID, action string, approved bool, userID string) {
	status := "✅ ĐÃ ĐƯỢC PHÊ DUYỆT"
	if !approved {
		status = "❌ ĐÃ BỊ TỪ CHỐI"
	}
	text := fmt.Sprintf("%s bởi Admin `%s` (Action: `%s`) cho yêu cầu `%s`.", status, userID, action, reqID)
	h.sendMessageToChat(chatID, text)
}

func (h *HITLCoordinator) notifyDecisionTimeout(chatID, reqID string) {
	text := fmt.Sprintf("⏱️ Yêu cầu phê duyệt bảo mật `%s` đã hết thời gian chờ và tự động bị hủy.", reqID)
	h.sendMessageToChat(chatID, text)
}

func (h *HITLCoordinator) isUserAdmin(userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" || h.cfg == nil {
		return false
	}
	for _, admin := range h.cfg.Zalo.AdminUserIDs {
		if strings.EqualFold(strings.TrimSpace(admin), userID) {
			return true
		}
	}
	for _, admin := range h.cfg.Security.AdminUserIDs {
		if fmt.Sprintf("%d", admin) == userID || strings.EqualFold(fmt.Sprintf("%d", admin), userID) {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseUserID(userID string) int64 {
	id, _ := strconv.ParseInt(userID, 10, 64)
	return id
}

