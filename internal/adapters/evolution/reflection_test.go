package evolution

import (
	"context"
	"testing"
	"time"

	"agyent/internal/core/domain"
)

func TestReflectionEngine_HeuristicFallback(t *testing.T) {
	engine := NewReflectionEngine(nil, nil, 5*time.Second)
	ctx := context.Background()

	snapshot := domain.ConversationSnapshot{
		AuditEntries: []domain.AuditLog{
			{Status: "ERROR", ErrorMessage: "exit status 1: undefined variable"},
		},
		Turns: []domain.CanonicalMessage{
			{Text: "Từ giờ hãy trả lời ngắn gọn và súc tích nhé."},
		},
	}

	abortChan := make(chan struct{})
	candidates, err := engine.Reflect(ctx, snapshot, abortChan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(candidates) < 2 {
		t.Fatalf("expected at least 2 candidates, got %d", len(candidates))
	}

	hasLesson := false
	hasPref := false
	for _, c := range candidates {
		if c.Category == domain.CategoryLesson {
			hasLesson = true
		}
		if c.Category == domain.CategoryPreference {
			hasPref = true
		}
	}

	if !hasLesson || !hasPref {
		t.Errorf("expected both lesson and preference candidates, got %+v", candidates)
	}
}

func TestReflectionEngine_AbortSignal(t *testing.T) {
	engine := NewReflectionEngine(nil, nil, 5*time.Second)
	ctx := context.Background()

	snapshot := domain.ConversationSnapshot{
		Turns: []domain.CanonicalMessage{
			{Text: "some turn"},
		},
	}

	abortChan := make(chan struct{})
	close(abortChan) // Abort immediately

	candidates, err := engine.Reflect(ctx, snapshot, abortChan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates on abort, got %d", len(candidates))
	}
}
