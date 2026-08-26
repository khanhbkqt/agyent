package evolution

import (
	"context"
	"testing"
	"time"

	"agyent/internal/config"
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
