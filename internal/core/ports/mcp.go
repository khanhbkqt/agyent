package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// MCPRegistryPort manages mounting and unmounting MCP server configurations to the runtime substrate.
type MCPRegistryPort interface {
	// MountServers registers MCP servers for a given active session with atomic synchronization.
	MountServers(ctx context.Context, sessionKey string, servers []domain.MCPServerConfig) error

	// UnmountServers removes or decrements reference count of MCP servers for a session upon turn completion.
	UnmountServers(ctx context.Context, sessionKey string, servers []domain.MCPServerConfig) error
}
