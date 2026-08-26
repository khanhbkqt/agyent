package evolution

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agyent/internal/core/concurrency/oslock"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.MemoryStorePort = (*MemoryStore)(nil)

// MemoryStore provides two-tier atomic persistence, OS file locking, and 4D memory promotion.
type MemoryStore struct {
	storage          ports.StoragePort
	conflictResolver ports.ConflictResolverPort
	compactor        *MemoryCompactor
}

// NewMemoryStore constructs a new MemoryStore instance.
func NewMemoryStore(storage ports.StoragePort, resolver ports.ConflictResolverPort) *MemoryStore {
	if resolver == nil {
		resolver = NewConflictResolver()
	}
	return &MemoryStore{
		storage:          storage,
		conflictResolver: resolver,
		compactor:        NewMemoryCompactor(),
	}
}

// AppendDailyLog writes an entry into memory/YYYY-MM-DD.md with atomic locking.
func (s *MemoryStore) AppendDailyLog(ctx context.Context, workspaceDir string, candidate domain.MemoryCandidate) error {
	if workspaceDir == "" {
		return fmt.Errorf("workspace directory cannot be empty")
	}

	memDir := filepath.Join(workspaceDir, "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		return fmt.Errorf("failed to create memory directory: %w", err)
	}

	loc := s.detectUserLocation(workspaceDir)
	now := time.Now().In(loc)
	todayStr := now.Format("2006-01-02")
	timeStr := now.Format("15:04:05")
	dailyFile := filepath.Join(memDir, fmt.Sprintf("%s.md", todayStr))
	lockFile := dailyFile + ".lock"

	tag := candidate.ConflictKey
	if tag == "" {
		tag = candidate.Title
	}
	if tag == "" {
		tag = string(candidate.Category)
	}

	entryLine := fmt.Sprintf("- [%s] [%s] (%s): %s\n", timeStr, strings.ToUpper(string(candidate.Category)), tag, candidate.Constraint)

	unlock, err := oslock.AcquireOSFileLock(ctx, lockFile, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to acquire lock for daily log: %w", err)
	}
	defer unlock()

	// Append directly to daily log
	f, err := os.OpenFile(dailyFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open daily log file %s: %w", dailyFile, err)
	}
	defer f.Close()

	if _, err := f.WriteString(entryLine); err != nil {
		return fmt.Errorf("failed to write daily log: %w", err)
	}
	_ = f.Sync()

	return nil
}

// PromoteToDurable4D merges candidates into 4D MEMORY.md using atomic write and OS file locking.
func (s *MemoryStore) PromoteToDurable4D(ctx context.Context, workspaceDir string, candidates []domain.MemoryCandidate) error {
	if workspaceDir == "" || len(candidates) == 0 {
		return nil
	}

	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		return fmt.Errorf("failed to create workspace dir: %w", err)
	}

	memoryFile := filepath.Join(workspaceDir, "MEMORY.md")
	lockFile := memoryFile + ".lock"
	tmpFile := memoryFile + ".tmp"

	unlock, err := oslock.AcquireOSFileLock(ctx, lockFile, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to acquire lock for MEMORY.md: %w", err)
	}
	defer unlock()

	var existingContent string
	if data, err := os.ReadFile(memoryFile); err == nil {
		existingContent = string(data)
	}

	merged, _, err := s.conflictResolver.ResolveAndMerge4D(existingContent, candidates)
	if err != nil {
		return fmt.Errorf("failed to resolve rule conflicts: %w", err)
	}

	// Apply Compaction if needed
	compacted, _, _ := s.compactor.Compact(merged, 200)

	// Write to temporary file with fsync
	tmpF, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open tmp memory file: %w", err)
	}

	if _, err := tmpF.WriteString(compacted); err != nil {
		_ = tmpF.Close()
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to write tmp memory file: %w", err)
	}
	_ = tmpF.Sync()
	_ = tmpF.Close()

	// Atomic Rename with Windows file lock retry backoff (P5)
	if err := atomicRenameWithRetry(tmpFile, memoryFile); err != nil {
		return fmt.Errorf("failed to atomic rename %s to %s: %w", tmpFile, memoryFile, err)
	}

	return nil
}

// CompactDurableMemory deduplicates and summarizes MEMORY.md if line limit exceeded.
func (s *MemoryStore) CompactDurableMemory(ctx context.Context, workspaceDir string, lineLimit int) error {
	if workspaceDir == "" {
		return nil
	}

	memoryFile := filepath.Join(workspaceDir, "MEMORY.md")
	if _, err := os.Stat(memoryFile); os.IsNotExist(err) {
		return nil
	}

	lockFile := memoryFile + ".lock"
	tmpFile := memoryFile + ".tmp"

	unlock, err := oslock.AcquireOSFileLock(ctx, lockFile, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to acquire lock for compaction: %w", err)
	}
	defer unlock()

	data, err := os.ReadFile(memoryFile)
	if err != nil {
		return err
	}

	compacted, wasCompacted, err := s.compactor.Compact(string(data), lineLimit)
	if err != nil || !wasCompacted {
		return err
	}

	tmpF, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, _ = tmpF.WriteString(compacted)
	_ = tmpF.Sync()
	_ = tmpF.Close()

	return atomicRenameWithRetry(tmpFile, memoryFile)
}

func atomicRenameWithRetry(src, dst string) error {
	var err error
	for i := 0; i < 3; i++ {
		err = os.Rename(src, dst)
		if err == nil {
			return nil
		}
		time.Sleep(time.Duration(10*(i+1)) * time.Millisecond)
	}
	return err
}

// UpdateReflectedStep updates the last_reflected_step cursor in database storage.
func (s *MemoryStore) UpdateReflectedStep(ctx context.Context, conversationID string, step int) error {
	if s.storage == nil || conversationID == "" {
		return nil
	}
	return s.storage.UpdateConversationReflectedStep(ctx, conversationID, step)
}

func (s *MemoryStore) detectUserLocation(dir string) *time.Location {
	userFile := filepath.Join(dir, "USER.md")
	if data, err := os.ReadFile(userFile); err == nil {
		return domain.ParseLocationFromText(string(data))
	}
	return time.Local
}
