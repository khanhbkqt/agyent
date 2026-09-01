package zalo

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

type pendingHITL struct {
	req        domain.ApprovalRequest
	respChan   chan domain.ApprovalDecision
	resolved   atomic.Bool
	cardChatID string
	createdAt  time.Time
}

// HITLCoordinator coordinates Human-in-the-Loop approval requests over Zalo.
type HITLCoordinator struct {
	client  *Client
	cfg     *config.Config
	pending sync.Map // map[string]*pendingHITL (key: requestID)
}

// NewHITLCoordinator initializes HITL approval management for Zalo.
func NewHITLCoordinator(client *Client, cfg *config.Config) *HITLCoordinator {
	return &HITLCoordinator{
		client: client,
		cfg:    cfg,
	}
}

// RequestApproval sends an interactive card and suspends execution until user action or timeout.
func (h *HITLCoordinator) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
	chatID := domain.ExtractChatIDFromSessionKey(req.SessionKey)
	if chatID == "" && h.cfg != nil {
		chatID = h.cfg.Zalo.GroupID
	}

	cardText := h.formatApprovalCard(req)

	respChan := make(chan domain.ApprovalDecision, 1)
	entry := &pendingHITL{
		req:        req,
		respChan:   respChan,
		cardChatID: chatID,
		createdAt:  time.Now(),
	}
	h.pending.Store(req.RequestID, entry)
	defer h.pending.Delete(req.RequestID)

	if h.client != nil && chatID != "" {
		_, sendErr := h.client.SendMessage(ctx, SendMessageRequest{
			ChatID:    chatID,
			Text:      cardText,
			ParseMode: "markdown",
		})
		if sendErr != nil {
			slog.ErrorContext(ctx, "failed to send Zalo HITL approval card", "error", sendErr, "req_id", req.RequestID)
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
				Action:    "timeout",
				Approved:  false,
				Timestamp: time.Now(),
			}, nil
		}

	case <-ctx.Done():
		if entry.resolved.CompareAndSwap(false, true) {
			return domain.ApprovalDecision{
				RequestID: req.RequestID,
				Action:    "cancelled",
				Approved:  false,
				Timestamp: time.Now(),
			}, ctx.Err()
		}
	}

	return domain.ApprovalDecision{
		RequestID: req.RequestID,
		Action:    "unknown",
		Approved:  false,
		Timestamp: time.Now(),
	}, nil
}

// HandleCommandApproval processes text commands (/approve <req_id> or /deny <req_id>).
func (h *HITLCoordinator) HandleCommandApproval(ctx context.Context, reqID, userID string, approved bool) error {
	val, exists := h.pending.Load(reqID)
	if !exists {
		return fmt.Errorf("yêu cầu phê duyệt `%s` không tồn tại hoặc đã hết hạn", reqID)
	}
	entry := val.(*pendingHITL)

	// RBAC Check
	if !h.isUserAdmin(userID) {
		return fmt.Errorf("⛔ Bạn không có quyền quản trị để phê duyệt yêu cầu này")
	}

	if !entry.resolved.CompareAndSwap(false, true) {
		return fmt.Errorf("yêu cầu này đã được xử lý trước đó")
	}

	action := "allow_once"
	if !approved {
		action = "deny"
	}

	decision := domain.ApprovalDecision{
		RequestID: reqID,
		Action:    action,
		Approved:  approved,
		Timestamp: time.Now(),
	}

	select {
	case entry.respChan <- decision:
	default:
	}

	h.notifyDecisionResult(entry.cardChatID, reqID, approved, userID)
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
	approved := act == "allow_once" || act == "allow_session"
	return h.HandleCommandApproval(ctx, reqID, fmt.Sprintf("%d", userID), approved)
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

// CancelPendingRequestsForSession terminates all pending approval requests for a given session.
func (h *HITLCoordinator) CancelPendingRequestsForSession(sessionKey string) {
	h.pending.Range(func(key, value interface{}) bool {
		entry := value.(*pendingHITL)
		if entry.req.SessionKey == sessionKey {
			h.CancelPendingRequest(entry.req.RequestID)
		}
		return true
	})
}

func (h *HITLCoordinator) formatApprovalCard(req domain.ApprovalRequest) string {
	f := NewFormatter()
	f.Heading("🛡️ YÊU CẦU PHÊ DUYỆT BẢO MẬT (HITL)", 1)
	f.Bold(fmt.Sprintf("Mã yêu cầu: `%s`", req.RequestID)).NewLine()
	f.ListItem(fmt.Sprintf("Công cụ (Tool): **%s**", req.ToolName))
	if req.CommandLine != "" {
		f.ListItem(fmt.Sprintf("Lệnh thực thi: `%s`", req.CommandLine))
	}
	if req.TargetFile != "" {
		f.ListItem(fmt.Sprintf("Tập tin: `%s`", req.TargetFile))
	}
	if req.Reason != "" {
		f.ListItem(fmt.Sprintf("Lý do: %s", req.Reason))
	}
	f.ListItem(fmt.Sprintf("Mức độ rủi ro: **%s**", req.RiskLevel))
	f.NewLine()
	f.Divider()
	f.Bold("👉 HƯỚNG DẪN QUẢN TRỊ VIÊN:").NewLine()
	f.Text(fmt.Sprintf("• Phê duyệt cho phép: `/approve %s`\n", req.RequestID))
	f.Text(fmt.Sprintf("• Từ chối hành động: `/deny %s`\n", req.RequestID))
	f.NewLine()
	f.Italic("⏱️ Yêu cầu sẽ tự động hủy nếu không có phản hồi trong 60 giây.")
	return f.BuildMarkdown()
}

func (h *HITLCoordinator) notifyDecisionResult(chatID, reqID string, approved bool, userID string) {
	if h.client == nil || chatID == "" {
		return
	}
	status := "✅ ĐÃ ĐƯỢC PHÊ DUYỆT"
	if !approved {
		status = "❌ ĐÃ BỊ TỪ CHỐI"
	}
	text := fmt.Sprintf("%s bởi Admin `%s` cho yêu cầu `%s`.", status, userID, reqID)
	_, _ = h.client.SendMessage(context.Background(), SendMessageRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "markdown",
	})
}

func (h *HITLCoordinator) notifyDecisionTimeout(chatID, reqID string) {
	if h.client == nil || chatID == "" {
		return
	}
	text := fmt.Sprintf("⏱️ Yêu cầu phê duyệt bảo mật `%s` đã hết hạn và tự động bị hủy.", reqID)
	_, _ = h.client.SendMessage(context.Background(), SendMessageRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "markdown",
	})
}

func (h *HITLCoordinator) isUserAdmin(userID string) bool {
	if h.cfg == nil {
		return false
	}
	for _, admin := range h.cfg.Zalo.AdminUserIDs {
		if strings.EqualFold(strings.TrimSpace(admin), strings.TrimSpace(userID)) {
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
