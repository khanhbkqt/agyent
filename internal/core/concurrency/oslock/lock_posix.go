//go:build !windows

package oslock

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

// AcquireOSFileLock attempts to acquire an exclusive lock on lockPath using POSIX flock with context cancellation and timeout.
func AcquireOSFileLock(ctx context.Context, lockPath string, timeout time.Duration) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", lockPath, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		default:
		}

		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}

		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("timeout waiting for lock on %s after %v: %w", lockPath, timeout, err)
		}

		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
