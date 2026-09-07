package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// MCPRegistryPort manages mounting and unmounting MCP server configurations to the runtime substrate.
type MCPRegistryPort interface {
	// AcquireExclusiveTurn reserves an MCP lease for one executing turn.
	// When a sessionKey is provided, it acquires a session-scoped lease allowing
	// concurrent execution across distinct sessions/workspaces.
	// Implementations must release the lease on process exit/crash.
	AcquireExclusiveTurn(ctx context.Context, sessionKey ...string) (release func(), err error)

	// MountServers registers MCP servers for a given active session with atomic synchronization.
	MountServers(ctx context.Context, sessionKey string, servers []domain.MCPServerConfig) error

	// UnmountServers removes or decrements reference count of MCP servers for a session upon turn completion.
	UnmountServers(ctx context.Context, sessionKey string, servers []domain.MCPServerConfig) error
}
