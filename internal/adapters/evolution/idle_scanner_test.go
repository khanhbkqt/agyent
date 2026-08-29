package evolution

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/core/domain"
)

type mockOrchestrator struct {
	triggeredCount int
	lastConvID     string
	lastTrigger    domain.EvolutionTrigger
}

func (m *mockOrchestrator) Start(ctx context.Context) error { return nil }
func (m *mockOrchestrator) Stop(ctx context.Context) error  { return nil }
func (m *mockOrchestrator) TriggerConversationEvolution(ctx context.Context, convID string, trigger domain.EvolutionTrigger) error {
	m.triggeredCount++
	m.lastConvID = convID
	m.lastTrigger = trigger
	return nil
}
func (m *mockOrchestrator) NotifyUserActivity(sessionKey string) {}

func TestIdleScanner_Lifecycle(t *testing.T) {
	mockOrch := &mockOrchestrator{}
	scanner := NewIdleScanner(nil, mockOrch, 15, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scanner.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	scanner.Stop()
}

func TestIdleScanner_ScanAndTrigger_WithRealSQLite(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "idle_scanner_test.db")

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}
	defer store.Close()

	// 1. Setup Agent & Session
	agent := &domain.Agent{
		Name:          "agyent",
		Status:        domain.StatusInitialized,
		WorkspacePath: tempDir,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := store.SaveAgent(ctx, agent); err != nil {
		t.Fatalf("failed to save agent: %v", err)
	}

	sessionKey := "telegram:8544450322"
	if _, err := store.GetOrCreateSession(ctx, sessionKey, agent.Name); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	now := time.Now()
	twentyMinsAgo := now.Add(-20 * time.Minute)
	twoMinsAgo := now.Add(-2 * time.Minute)

	// 2. Setup Conversations with different lifecycle states
	// A: Idle > 15m and has unreflected turns (Should Trigger)
	cReady := &domain.Conversation{
		ID:                "conv-idle-ready",
		SessionKey:        sessionKey,
		AgentName:         agent.Name,
		TurnCount:         5,
		LastReflectedStep: 0,
		IsPinned:          false,
		IsArchived:        false,
		CreatedAt:         twentyMinsAgo,
		UpdatedAt:         twentyMinsAgo,
	}

	// B: Idle > 15m but already reflected (Should Skip)
	cReflected := &domain.Conversation{
		ID:                "conv-idle-reflected",
		SessionKey:        sessionKey,
		AgentName:         agent.Name,
		TurnCount:         5,
		LastReflectedStep: 5,
		IsPinned:          false,
		IsArchived:        false,
		CreatedAt:         twentyMinsAgo,
		UpdatedAt:         twentyMinsAgo,
	}

	// C: Idle > 15m but 0 turns (Should Skip)
	cZeroTurns := &domain.Conversation{
		ID:                "conv-idle-zero",
		SessionKey:        sessionKey,
		AgentName:         agent.Name,
		TurnCount:         0,
		LastReflectedStep: 0,
		IsPinned:          false,
		IsArchived:        false,
		CreatedAt:         twentyMinsAgo,
		UpdatedAt:         twentyMinsAgo,
	}

	// D: Recent active (< 15m) (Should Skip)
	cRecent := &domain.Conversation{
		ID:                "conv-recent-active",
		SessionKey:        sessionKey,
		AgentName:         agent.Name,
		TurnCount:         3,
		LastReflectedStep: 0,
		IsPinned:          false,
		IsArchived:        false,
		CreatedAt:         twoMinsAgo,
		UpdatedAt:         twoMinsAgo,
	}

	// E: Archived (Should Skip)
	cArchived := &domain.Conversation{
		ID:                "conv-archived",
		SessionKey:        sessionKey,
		AgentName:         agent.Name,
		TurnCount:         5,
		LastReflectedStep: 0,
		IsPinned:          false,
		IsArchived:        true,
		CreatedAt:         twentyMinsAgo,
		UpdatedAt:         twentyMinsAgo,
	}

	for _, c := range []*domain.Conversation{cReady, cReflected, cZeroTurns, cRecent, cArchived} {
		if err := store.SaveConversation(ctx, c); err != nil {
			t.Fatalf("failed to save conversation %s: %v", c.ID, err)
		}
	}

	// 3. Execute ScanNow
	mockOrch := &mockOrchestrator{}
	scanner := NewIdleScanner(store, mockOrch, 15, 1)

	scanner.ScanNow(ctx)

	// 4. Assertions
	if mockOrch.triggeredCount != 1 {
		t.Fatalf("expected exactly 1 triggered evolution, got %d", mockOrch.triggeredCount)
	}
	if mockOrch.lastConvID != "conv-idle-ready" {
		t.Errorf("expected conv-idle-ready to be triggered, got %s", mockOrch.lastConvID)
	}
	if mockOrch.lastTrigger != domain.TriggerIdleTimeout {
		t.Errorf("expected trigger TriggerIdleTimeout, got %s", mockOrch.lastTrigger)
	}
}
