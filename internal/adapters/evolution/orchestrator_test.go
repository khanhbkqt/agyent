package evolution

import (
	"context"
	"testing"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

func TestEvolutionOrchestrator_Lifecycle(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Evolution.Enabled = true

	orch := NewEvolutionOrchestrator(cfg, nil, nil)
	ctx := context.Background()

	if err := orch.Start(ctx); err != nil {
		t.Fatalf("failed to start orchestrator: %v", err)
	}

	orch.NotifyUserActivity("telegram:123456")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := orch.Stop(shutdownCtx); err != nil {
		t.Fatalf("failed to stop orchestrator: %v", err)
	}
}

type mockStorageForEvolution struct {
	ports.StoragePort
}

func (m *mockStorageForEvolution) GetConversation(ctx context.Context, id string) (*domain.Conversation, error) {
	return &domain.Conversation{
		ID:         id,
		SessionKey: "telegram:123456",
		AgentName:  "agyent",
	}, nil
}

func (m *mockStorageForEvolution) ListAuditLogs(ctx context.Context, sessionKey string, limit int) ([]domain.AuditLog, error) {
	return nil, nil
}

func (m *mockStorageForEvolution) UpdateConversationReflectedStep(ctx context.Context, convID string, step int) error {
	return nil
}

func TestEvolutionOrchestrator_TriggerAfterUserActivity_NoPanic(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Evolution.Enabled = true

	mockStore := &mockStorageForEvolution{}
	orch := NewEvolutionOrchestrator(cfg, mockStore, nil)
	ctx := context.Background()

	if err := orch.Start(ctx); err != nil {
		t.Fatalf("failed to start orchestrator: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = orch.Stop(shutdownCtx)
	}()

	// 1. Notify activity -> stores sessionGen in genMap
	orch.NotifyUserActivity("telegram:123456")

	// 2. Trigger evolution -> must NOT panic on sessionGen
	err := orch.TriggerConversationEvolution(ctx, "conv-test-123", domain.TriggerExplicitSwitch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
