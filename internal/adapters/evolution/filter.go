package evolution

import (
	"context"
	"strings"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.HeuristicFilterPort = (*HeuristicFilter)(nil)

// HeuristicFilter provides high-speed, Zero-LLM pre-filtering of conversation snapshots.
type HeuristicFilter struct {
	confidenceThreshold float64
}

// NewHeuristicFilter creates a new HeuristicFilter with the given confidence threshold.
func NewHeuristicFilter(confidenceThreshold float64) *HeuristicFilter {
	if confidenceThreshold <= 0 {
		confidenceThreshold = 0.85
	}
	return &HeuristicFilter{
		confidenceThreshold: confidenceThreshold,
	}
}

// ShouldReflect inspects audit logs and conversation messages for learning signals using fast lexical heuristics.
func (f *HeuristicFilter) ShouldReflect(ctx context.Context, snapshot domain.ConversationSnapshot) (bool, float64, string) {
	// 1. Audit Error Check: If any turn failed, it's a prime target for Root Cause analysis
	for _, audit := range snapshot.AuditEntries {
		if strings.EqualFold(audit.Status, "ERROR") || audit.ErrorMessage != "" {
			return true, 0.92, "audit_error_detected"
		}
	}

	// 2. Scan User Messages in Snapshot Turns
	correctionKeywords := []string{
		"wrong", "mistake", "error", "incorrect", "from now on", "always", "never",
		"should be", "must be", "remember to", "rule", "next time", "fix this", "do not use",
		"sai rồi", "bị lỗi", "không đúng", "phải là", "từ giờ", "luôn luôn", "đừng bao giờ",
		"chưa đúng", "không phải", "chuẩn rồi", "tốt lắm", "nhớ là", "quy tắc", "lần sau",
		"chỉnh lại", "sửa lại", "không được", "đừng dùng", "hãy dùng", "yêu cầu",
	}

	adrKeywords := []string{
		"adr", "architecture", "clean architecture", "decision", "standardize", "convention",
		"refactor", "replace", "principles", "ports and adapters", "pattern",
		"kiến trúc", "thống nhất", "quyết định", "thay thế", "nguyên lý", "quy ước",
	}

	preferenceKeywords := []string{
		"concise", "brief", "short response", "no fluff", "use markdown", "diff only",
		"tone", "format", "preference",
		"xưng em", "gọi anh", "trả lời ngắn", "trả lời gọn", "súc tích",
		"không giải thích dài", "viết tiếng việt", "dùng markdown", "diff",
	}

	for _, turn := range snapshot.Turns {
		text := strings.ToLower(turn.Text)

		// Check for Correction Signals
		for _, kw := range correctionKeywords {
			if strings.Contains(text, kw) {
				return true, 0.90, "correction_signal_detected: " + kw
			}
		}

		// Check for ADR Signals
		for _, kw := range adrKeywords {
			if strings.Contains(text, kw) {
				return true, 0.88, "adr_signal_detected: " + kw
			}
		}

		// Check for Preference Signals
		for _, kw := range preferenceKeywords {
			if strings.Contains(text, kw) {
				return true, 0.89, "preference_signal_detected: " + kw
			}
		}
	}

	// 3. No learning signals found (Simple Q&A, greetings, trivia) -> Skip (0 Token cost)
	return false, 0.10, "no_learning_signal"
}
