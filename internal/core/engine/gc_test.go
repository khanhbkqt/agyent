package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/core/domain"
)

func TestSafePurgeBrainDir(t *testing.T) {
	tempHome := t.TempDir()
	brainRootDir := filepath.Join(tempHome, ".gemini", "antigravity", "brain")
	_ = os.MkdirAll(brainRootDir, 0755)

	validUUID := "f8b6c9bf-55ad-405a-90d8-7a96305a1227"
	targetDir := filepath.Join(brainRootDir, validUUID)
	_ = os.MkdirAll(targetDir, 0755)
	_ = os.WriteFile(filepath.Join(targetDir, "transcript.jsonl"), []byte("test"), 0644)

	// 1. Success case: valid UUID inside root
	if err := SafePurgeBrainDir(brainRootDir, validUUID); err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Errorf("expected directory to be deleted")
	}

	// 2. Reject empty or invalid UUID
	if err := SafePurgeBrainDir(brainRootDir, ""); err == nil {
		t.Errorf("expected error for empty UUID")
	}
	if err := SafePurgeBrainDir(brainRootDir, "invalid-uuid"); err == nil {
		t.Errorf("expected error for non-UUID string")
	}

	// 3. Reject path traversal attempts
	if err := SafePurgeBrainDir(brainRootDir, "../../../etc/passwd"); err == nil {
		t.Errorf("expected error for traversal string")
	}
}

func TestEngine_ConversationGC(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_gc.db")

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	eng := &Engine{
		storage: store,
		ctx:     ctx,
	}

	// Create test agent and session
	agent := &domain.Agent{
		Name:          "gc_agent",
		Description:   "GC Agent",
		Status:        domain.StatusInitialized,
		WorkspacePath: tempDir,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	_ = store.SaveAgent(ctx, agent)

	sessionKey := "telegram:999"
	_ = store.TouchConversation(ctx, sessionKey, agent.Name, "", "f8b6c9bf-55ad-405a-90d8-7a96305a1111", "Hello")
	_ = store.SetConversationArchived(ctx, "f8b6c9bf-55ad-405a-90d8-7a96305a1111", true)

	// Purge with 0 days cutoff
	count, err := eng.RunConversationGC(ctx, 0)
	if err != nil {
		t.Fatalf("failed to run GC: %v", err)
	}
	if count < 0 {
		t.Errorf("unexpected negative count: %d", count)
	}

	// Verify conversation is gone
	_, err = store.GetConversation(ctx, "f8b6c9bf-55ad-405a-90d8-7a96305a1111")
	if err == nil {
		t.Errorf("expected error getting purged conversation, got nil")
	}
}
