package evolution

import (
	"context"
	"testing"
	"time"

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
