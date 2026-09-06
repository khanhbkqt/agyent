package ports

import (
	"context"
	"errors"

	"agyent/internal/core/domain"
)

var (
	// ErrExecutionRefused indicates that execution was refused due to missing admission, revoked mapping, or uninitialized policy/security.
	ErrExecutionRefused = errors.New("execution refused")
)

// ExecutionServicePort defines the unified, authorized execution chokepoint for all AGY CLI operations.
type ExecutionServicePort interface {
	// ExecuteTurn authorizes, generates TurnID, binds TurnSecurityContext, isolates MCP, and executes AGY turn.
	ExecuteTurn(ctx context.Context, principal domain.Principal, req domain.ExecutionRequest, sessionKey string, isStreaming bool) (*domain.ExecutionResult, error)

	// InterruptTurn signals an active turn identified by sessionKey to interrupt gracefully.
	InterruptTurn(ctx context.Context, sessionKey string) error
}
