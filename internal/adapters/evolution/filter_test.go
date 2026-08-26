package evolution

import (
	"context"
	"testing"

	"agyent/internal/core/domain"
)

func TestHeuristicFilter_ShouldReflect(t *testing.T) {
	filter := NewHeuristicFilter(0.85)
	ctx := context.Background()

	tests := []struct {
		name         string
		snapshot     domain.ConversationSnapshot
		wantReflect  bool
		minScore     float64
		matchPattern string
	}{
		{
			name: "Simple Q&A greeting -> Skip",
			snapshot: domain.ConversationSnapshot{
				Turns: []domain.CanonicalMessage{
					{Text: "Chào em, hôm nay thời tiết thế nào?"},
					{Text: "Cảm ơn em nhé!"},
				},
			},
			wantReflect: false,
			minScore:    0.0,
		},
		{
			name: "Audit error entry -> High confidence reflect",
			snapshot: domain.ConversationSnapshot{
				AuditEntries: []domain.AuditLog{
					{Status: "ERROR", ErrorMessage: "exit status 1: compiler error"},
				},
			},
			wantReflect:  true,
			minScore:     0.85,
			matchPattern: "audit_error_detected",
		},
		{
			name: "Explicit correction keyword -> Reflect",
			snapshot: domain.ConversationSnapshot{
				Turns: []domain.CanonicalMessage{
					{Text: "Em làm sai rồi, chỗ này phải dùng int64 chứ!"},
				},
			},
			wantReflect:  true,
			minScore:     0.85,
			matchPattern: "correction_signal_detected",
		},
		{
			name: "Architecture Decision keyword -> Reflect",
			snapshot: domain.ConversationSnapshot{
				Turns: []domain.CanonicalMessage{
					{Text: "Thống nhất dự án này tuân thủ Clean Architecture và Ports & Adapters."},
				},
			},
			wantReflect:  true,
			minScore:     0.85,
			matchPattern: "adr_signal_detected",
		},
		{
			name: "User preference keyword -> Reflect",
			snapshot: domain.ConversationSnapshot{
				Turns: []domain.CanonicalMessage{
					{Text: "Từ giờ trả lời ngắn gọn thôi nhé, không giải thích dài."},
				},
			},
			wantReflect:  true,
			minScore:     0.85,
			matchPattern: "preference_signal_detected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			should, score, reason := filter.ShouldReflect(ctx, tt.snapshot)
			if should != tt.wantReflect {
				t.Errorf("ShouldReflect() got %v, want %v (reason: %s)", should, tt.wantReflect, reason)
			}
			if tt.wantReflect && score < tt.minScore {
				t.Errorf("Score %f is lower than min expected %f", score, tt.minScore)
			}
		})
	}
}
