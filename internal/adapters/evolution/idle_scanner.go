package evolution

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"agyent/internal/core/concurrency"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// IdleScanner scans conversations periodically to trigger evolution on idle sessions.
type IdleScanner struct {
	storage      ports.StoragePort
	orchestrator ports.EvolutionOrchestratorPort
	idleTimeout  time.Duration
	scanInterval time.Duration
	running      bool
	stopChan     chan struct{}
	mu           sync.Mutex
	wg           sync.WaitGroup
}

// NewIdleScanner constructs a new IdleScanner instance.
func NewIdleScanner(storage ports.StoragePort, orchestrator ports.EvolutionOrchestratorPort, idleMinutes, scanMinutes int) *IdleScanner {
	if idleMinutes <= 0 {
		idleMinutes = 15
	}
	if scanMinutes <= 0 {
		scanMinutes = 5
	}

	return &IdleScanner{
		storage:      storage,
		orchestrator: orchestrator,
		idleTimeout:  time.Duration(idleMinutes) * time.Minute,
		scanInterval: time.Duration(scanMinutes) * time.Minute,
		stopChan:     make(chan struct{}),
	}
}

// Start begins background periodic scanning for idle conversations.
func (s *IdleScanner) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopChan = make(chan struct{})
	s.wg.Add(1)
	s.mu.Unlock()

	concurrency.SafeGo(func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.scanInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopChan:
				return
			case <-ticker.C:
				s.scanAndTrigger(ctx)
			}
		}
	})
}

// Stop terminates the idle scanner gracefully.
func (s *IdleScanner) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopChan)
	s.mu.Unlock()

	s.wg.Wait()
}

// ScanNow triggers an immediate synchronous scan iteration (useful for tests and maintenance).
func (s *IdleScanner) ScanNow(ctx context.Context) {
	s.scanAndTrigger(ctx)
}

func (s *IdleScanner) scanAndTrigger(ctx context.Context) {
	if s.storage == nil || s.orchestrator == nil {
		return
	}

	// List recent active conversations
	convs, _, err := s.storage.ListRecentConversations(ctx, "", "", "", 50, 0)
	if err != nil {
		// If listing global is not scoped, scan is no-op
		return
	}

	now := time.Now()
	cutoff := now.Add(-s.idleTimeout)

	for _, conv := range convs {
		if conv.IsArchived {
			continue
		}

		// Check if updated before cutoff and has un-reflected turns
		if conv.UpdatedAt.Before(cutoff) && conv.TurnCount > conv.LastReflectedStep {
			slog.DebugContext(ctx, "IdleScanner detected idle conversation ready for reflection",
				slog.String("conversation_id", conv.ID),
				slog.String("session_key", conv.SessionKey),
				slog.Time("updated_at", conv.UpdatedAt),
			)
			_ = s.orchestrator.TriggerConversationEvolution(ctx, conv.ID, domain.TriggerIdleTimeout)
		}
	}
}
