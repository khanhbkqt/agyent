package ports

import (
	"context"
	"time"
)

// UnlockFunc releases the acquired session lock.
type UnlockFunc func()

// LockManager coordinates mutual exclusion per session key.
type LockManager interface {
	// Acquire blocks until lock is granted, timeout expires, or context is canceled.
	Acquire(ctx context.Context, sessionKey string, timeout time.Duration) (UnlockFunc, error)

	// ActiveLockCount returns the number of active tracked locks (for metrics & diagnostics).
	ActiveLockCount() int

	// ForceUnlock unconditionally releases any held lock for the session key and resets lock entry.
	// Returns true if a lock was actively held and released, false otherwise.
	ForceUnlock(sessionKey string) bool
}
