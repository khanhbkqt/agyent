package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// ExecutionServicePort defines the unified, authorized execution chokepoint for all AGY CLI operations.
type ExecutionServicePort interface {
	// ExecuteTurn authorizes, generates TurnID, binds TurnSecurityContext, isolates MCP, and executes AGY turn.
	ExecuteTurn(ctx context.Context, principal domain.Principal, req domain.ExecutionRequest, sessionKey string, isStreaming bool) (*domain.ExecutionResult, error)

	// InterruptTurn signals an active turn identified by sessionKey to interrupt gracefully.
	InterruptTurn(ctx context.Context, sessionKey string) error
}
