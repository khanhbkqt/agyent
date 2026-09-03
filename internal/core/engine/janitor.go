package engine

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agyent/internal/core/concurrency"
)

// StartBackgroundJanitor launches a recurring background housekeeping routine that cleans
// stale file locks and reaps abandoned artifacts to prevent session contention and 5-minute hangs.
func (e *Engine) StartBackgroundJanitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Minute
	}

	concurrency.SafeGo(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Run immediate clean pass on boot
		e.runJanitorPass(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.runJanitorPass(ctx)
			}
		}
	})
}

func (e *Engine) runJanitorPass(ctx context.Context) {
	slog.DebugContext(ctx, "Running autonomous background janitor pass")
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}

	// 1. Purge stale Antigravity presence locks (~/.gemini/antigravity-cli/presence/*.lock)
	presenceDirs := []string{
		filepath.Join(homeDir, ".gemini", "antigravity-cli", "presence"),
		filepath.Join(homeDir, ".gemini", "antigravity", "presence"),
	}
	purgedLocks := 0
	for _, pDir := range presenceDirs {
		entries, err := os.ReadDir(pDir)
		if err == nil {
			for _, ent := range entries {
				if strings.HasSuffix(ent.Name(), ".lock") {
					fullPath := filepath.Join(pDir, ent.Name())
					info, err := ent.Info()
					if err == nil && (info.Size() == 0 || time.Since(info.ModTime()) > 1*time.Hour) {
						if err := os.Remove(fullPath); err == nil {
							purgedLocks++
						}
					}
				}
			}
		}
	}

	// 2. Purge stale Camoufox profile locks
	camoufoxProfilesDir := filepath.Join(homeDir, ".agyent", "camoufox", "profiles")
	profEntries, err := os.ReadDir(camoufoxProfilesDir)
	if err == nil {
		for _, pe := range profEntries {
			if pe.IsDir() {
				locks := []string{
					filepath.Join(camoufoxProfilesDir, pe.Name(), "data_dir", "parent.lock"),
					filepath.Join(camoufoxProfilesDir, pe.Name(), "data_dir", ".parentlock"),
					filepath.Join(camoufoxProfilesDir, pe.Name(), "data_dir", "lock"),
				}
				for _, l := range locks {
					info, err := os.Stat(l)
					if err == nil && time.Since(info.ModTime()) > 30*time.Minute {
						if err := os.Remove(l); err == nil {
							purgedLocks++
						}
					}
				}
			}
		}
	}

	if purgedLocks > 0 {
		slog.InfoContext(ctx, "Janitor cleaned up stale lock files", slog.Int("purged_locks", purgedLocks))
	}
}
