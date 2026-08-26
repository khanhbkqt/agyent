package sqlite

import (
	"agyent/internal/core/concurrency"
)

var (
	// ErrLockTimeout is returned when waiting for a session lock exceeds the timeout duration.
	ErrLockTimeout = concurrency.ErrLockTimeout
	// ErrLockCanceled is returned when the context is canceled while waiting for a session lock.
	ErrLockCanceled = concurrency.ErrLockCanceled
)

// SessionLockManager is a type alias for concurrency.SessionLockManager for backwards compatibility.
type SessionLockManager = concurrency.SessionLockManager

// NewSessionLockManager creates a new SessionLockManager.
func NewSessionLockManager() *SessionLockManager {
	return concurrency.NewSessionLockManager()
}
