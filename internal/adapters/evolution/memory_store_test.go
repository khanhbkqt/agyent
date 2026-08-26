package evolution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agyent/internal/core/domain"
)

func TestMemoryStore_AppendDailyLog(t *testing.T) {
	tempDir := t.TempDir()
	store := NewMemoryStore(nil, nil)
	ctx := context.Background()

	cand := domain.MemoryCandidate{
		Category:    domain.CategoryLesson,
		Title:       "SQLite Lesson",
		Constraint:  "Always use FlexTime",
		ConflictKey: "sqlite_flextime",
	}

	if err := store.AppendDailyLog(ctx, tempDir, cand); err != nil {
		t.Fatalf("unexpected error appending daily log: %v", err)
	}

	todayStr := time.Now().Format("2006-01-02")
	dailyFile := filepath.Join(tempDir, "memory", todayStr+".md")

	data, err := os.ReadFile(dailyFile)
	if err != nil {
		t.Fatalf("failed to read daily log file: %v", err)
	}

	if !strings.Contains(string(data), "Always use FlexTime") {
		t.Errorf("expected daily log to contain constraint, got:\n%s", string(data))
	}
}

func TestMemoryStore_PromoteToDurable4D(t *testing.T) {
	tempDir := t.TempDir()
	store := NewMemoryStore(nil, nil)
	ctx := context.Background()

	candidates := []domain.MemoryCandidate{
		{
			Category:    domain.CategoryADR,
			Title:       "Architecture Decision",
			Constraint:  "Adopt Clean Architecture with Ports and Adapters",
			ConflictKey: "clean_arch",
			IsDurable:   true,
			CreatedAt:   time.Now(),
		},
		{
			Category:    domain.CategoryPreference,
			Title:       "Tone Preference",
			Constraint:  "Xưng em gọi anh Khánh lịch thiệp",
			ConflictKey: "communication_tone",
			IsDurable:   true,
			CreatedAt:   time.Now(),
		},
	}

	if err := store.PromoteToDurable4D(ctx, tempDir, candidates); err != nil {
		t.Fatalf("unexpected error promoting to durable: %v", err)
	}

	memoryFile := filepath.Join(tempDir, "MEMORY.md")
	data, err := os.ReadFile(memoryFile)
	if err != nil {
		t.Fatalf("failed to read MEMORY.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "Adopt Clean Architecture") {
		t.Errorf("expected ADR in MEMORY.md, got:\n%s", content)
	}
	if !strings.Contains(content, "Xưng em gọi anh Khánh") {
		t.Errorf("expected preference in MEMORY.md, got:\n%s", content)
	}
}
