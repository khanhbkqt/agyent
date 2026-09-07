package oslock

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAcquireOSFileLock_MutualExclusion(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "test.lock")

	// Acquire first lock
	unlock1, err := AcquireOSFileLock(context.Background(), lockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to acquire initial lock: %v", err)
	}

	// Attempt second lock (should timeout quickly)
	_, err = AcquireOSFileLock(context.Background(), lockPath, 100*time.Millisecond)
	if err == nil {
		t.Fatalf("expected timeout acquiring already locked file")
	}

	// Release first lock
	unlock1()

	// Third attempt should now succeed
	unlock2, err := AcquireOSFileLock(context.Background(), lockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to acquire lock after release: %v", err)
	}
	unlock2()
}

func TestAcquireOSFileLock_ContextCancellation(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "cancel.lock")

	// Acquire first lock
	unlock1, err := AcquireOSFileLock(context.Background(), lockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to acquire initial lock: %v", err)
	}
	defer unlock1()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = AcquireOSFileLock(ctx, lockPath, 10*time.Second)
	duration := time.Since(start)

	if err == nil {
		t.Fatalf("expected context cancellation error, got nil")
	}
	if duration > 1*time.Second {
		t.Errorf("expected immediate return on context cancellation, took %v", duration)
	}
}

func TestAcquireOSFileLock_ConcurrentAccess(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "concurrent.lock")
	counterPath := filepath.Join(tempDir, "counter.txt")
	_ = os.WriteFile(counterPath, []byte("0"), 0644)

	var wg sync.WaitGroup
	workers := 5
	iterations := 5

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				unlock, err := AcquireOSFileLock(context.Background(), lockPath, 10*time.Second)
				if err != nil {
					t.Errorf("worker failed to acquire lock: %v", err)
					return
				}
				// Simulate critical section
				time.Sleep(10 * time.Millisecond)
				unlock()
			}
		}()
	}

	wg.Wait()
}

func TestAcquireOSFileLock_AutoCreatesParentDir(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "deep", "nested", "subpath", "test.lock")

	unlock, err := AcquireOSFileLock(context.Background(), lockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("expected lock to create parent directory automatically, got err: %v", err)
	}
	defer unlock()

	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lock file to exist at %s", lockPath)
	}
}

