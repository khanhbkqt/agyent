package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// MCPRegistryPort manages mounting and unmounting MCP server configurations to the runtime substrate.
type MCPRegistryPort interface {
	// AcquireExclusiveTurn reserves the global MCP substrate for one executing
	// turn. Older AGY CLI versions expose only a process-global MCP config; the
	// lease must therefore cover mount -> subprocess execution -> unmount.
	// Implementations must release the lease on process exit/crash.
	AcquireExclusiveTurn(ctx context.Context) (release func(), err error)

	// MountServers registers MCP servers for a given active session with atomic synchronization.
	MountServers(ctx context.Context, sessionKey string, servers []domain.MCPServerConfig) error

	// UnmountServers removes or decrements reference count of MCP servers for a session upon turn completion.
	UnmountServers(ctx context.Context, sessionKey string, servers []domain.MCPServerConfig) error
}
