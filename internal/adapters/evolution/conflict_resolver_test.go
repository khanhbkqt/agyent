package evolution

import (
	"strings"
	"testing"
	"time"

	"agyent/internal/core/domain"
)

func TestConflictResolver_InPlaceReplacement(t *testing.T) {
	resolver := NewConflictResolver()

	initialDoc := `# MEMORY.md

## 1. User-Soul Synergy & Collaboration Protocol
- [response_verbosity]: Luôn giải thích thật chi tiết và cặn kẽ mọi vấn đề.

## 4. Evolved Behavioral Guardrails
- [sqlite_scanner]: Use standard scanner.
`

	candidates := []domain.MemoryCandidate{
		{
			Category:    domain.CategoryPreference,
			Title:       "Verbosity Update",
			Constraint:  "Từ giờ hãy trả lời thật ngắn gọn, súc tích, chỉ cung cấp diff.",
			ConflictKey: "response_verbosity",
			IsDurable:   true,
			CreatedAt:   time.Now(),
		},
	}

	merged, applied, err := resolver.ResolveAndMerge4D(initialDoc, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(applied) != 1 {
		t.Fatalf("expected 1 applied candidate, got %d", len(applied))
	}

	// Verify old rule was replaced
	if strings.Contains(merged, "Luôn giải thích thật chi tiết") {
		t.Errorf("expected old rule to be replaced, but it was found in:\n%s", merged)
	}

	// Verify new rule is present
	if !strings.Contains(merged, "Từ giờ hãy trả lời thật ngắn gọn") {
		t.Errorf("expected new rule to be present in:\n%s", merged)
	}

	// Verify [response_verbosity] tag count is exactly 1 (no duplicate rules!)
	count := strings.Count(merged, "[response_verbosity]")
	if count != 1 {
		t.Errorf("expected exactly 1 instance of [response_verbosity], got %d in:\n%s", count, merged)
	}
}

func TestConflictResolver_AppendNewSection(t *testing.T) {
	resolver := NewConflictResolver()

	initialDoc := `# MEMORY.md`

	candidates := []domain.MemoryCandidate{
		{
			Category:    domain.CategoryADR,
			Title:       "Pure Go SQLite",
			Constraint:  "Dùng Pure-Go SQLite WAL mode zero CGO.",
			ConflictKey: "sqlite_engine",
			IsDurable:   true,
		},
	}

	merged, applied, err := resolver.ResolveAndMerge4D(initialDoc, candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(applied) != 1 {
		t.Fatalf("expected 1 applied candidate, got %d", len(applied))
	}

	if !strings.Contains(merged, "Hardened Architectural Decisions") {
		t.Errorf("expected ADR section in:\n%s", merged)
	}
	if !strings.Contains(merged, "Pure-Go SQLite WAL mode") {
		t.Errorf("expected constraint in:\n%s", merged)
	}
}
