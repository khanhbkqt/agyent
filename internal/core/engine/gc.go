package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agyent/internal/core/concurrency"
)

var uuidRegex = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// SafePurgeBrainDir safely deletes a conversation's brain directory with strict UUID and traversal validation.
func SafePurgeBrainDir(brainRootDir, convID string) error {
	cleanID := strings.TrimSpace(convID)
	if cleanID == "" || cleanID == "." || cleanID == ".." {
		return errors.New("invalid conversation ID for purge")
	}

	if !uuidRegex.MatchString(cleanID) {
		return fmt.Errorf("refusing to purge non-UUID conversation directory: %q", cleanID)
	}

	if brainRootDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot resolve home dir: %w", err)
		}
		brainRootDir = filepath.Join(home, ".gemini", "antigravity", "brain")
	}

	targetDir := filepath.Join(brainRootDir, cleanID)
	cleanTarget := filepath.Clean(targetDir)
	cleanRoot := filepath.Clean(brainRootDir)

	// Defense-in-depth Path Traversal guard
	if !strings.HasPrefix(cleanTarget, cleanRoot+string(filepath.Separator)) {
		return fmt.Errorf("path traversal detected for purge target: %q", targetDir)
	}

	if _, err := os.Stat(cleanTarget); os.IsNotExist(err) {
		return nil // Already purged or never created
	}

	return os.RemoveAll(cleanTarget)
}

// StartConversationGCWorker starts the background conversation lifecycle and garbage collection worker.
func (e *Engine) StartConversationGCWorker(ctx context.Context, interval time.Duration, olderThanDays int) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if olderThanDays <= 0 {
		olderThanDays = 30
	}

	// Run once on startup asynchronously with WaitGroup tracking
	e.wg.Add(1)
	concurrency.SafeGo(func() {
		defer e.wg.Done()
		_, _ = e.RunConversationGC(ctx, olderThanDays)
	})

	// Periodic ticker
	e.wg.Add(1)
	concurrency.SafeGo(func() {
		defer e.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-e.ctx.Done():
				return
			case <-ticker.C:
				_, _ = e.RunConversationGC(ctx, olderThanDays)
			}
		}
	})
}

// RunConversationGC performs a single garbage collection cycle for expired archived conversations.
func (e *Engine) RunConversationGC(ctx context.Context, olderThanDays int) (int, error) {
	if e.storage == nil {
		return 0, nil
	}

	// 1. Query expired archived conversation IDs (read-only, decoupled from deletion lock)
	expiredIDs, err := e.storage.GetExpiredArchivedConversationIDs(ctx, olderThanDays)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to query expired archived conversations for GC", slog.String("error", err.Error()))
		return 0, err
	}

	if len(expiredIDs) == 0 {
		return 0, nil
	}

	slog.InfoContext(ctx, "Starting Conversation GC purge cycle", slog.Int("count", len(expiredIDs)))

	// 2. Delete database records in SQLite
	if err := e.storage.PurgeConversations(ctx, expiredIDs); err != nil {
		slog.ErrorContext(ctx, "Failed to purge conversation records from database", slog.String("error", err.Error()))
		return 0, err
	}

	// 3. Resolve brain directory for filesystem cleanup
	home, _ := os.UserHomeDir()
	brainRootDir := filepath.Join(home, ".gemini", "antigravity", "brain")

	// 4. Delete filesystem brain directories asynchronously with error suppression
	purgedCount := 0
	for _, id := range expiredIDs {
		if err := SafePurgeBrainDir(brainRootDir, id); err != nil {
			slog.WarnContext(ctx, "Failed to purge brain directory", slog.String("conversation_id", id), slog.String("error", err.Error()))
		} else {
			purgedCount++
		}
	}

	slog.InfoContext(ctx, "Conversation GC purge cycle completed", slog.Int("purged_db", len(expiredIDs)), slog.Int("purged_disk", purgedCount))
	return purgedCount, nil
}
