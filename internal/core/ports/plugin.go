package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// PluginManagerPort defines the management and execution contract for the Plugin Ecosystem.
type PluginManagerPort interface {
	// ListPlugins scans and returns all discovered plugins across global and workspace scopes.
	ListPlugins(ctx context.Context, globalHome string, workspaceDir string) ([]domain.Plugin, error)

	// TogglePlugin enables or disables a specific plugin in its manifest or local config.
	TogglePlugin(ctx context.Context, pluginName string, enabled bool, scope domain.ContextScope, workspaceDir string) error

	// InstallBuiltinPlugin copies a pre-packaged plugin from the builtin repository to the target scope.
	InstallBuiltinPlugin(ctx context.Context, pluginName string, targetScope domain.ContextScope, workspaceDir string) error

	// ValidatePlugin verifies that the runtime dependencies (e.g. binaries in PATH) for the plugin are satisfied.
	ValidatePlugin(ctx context.Context, plugin *domain.Plugin) error

	// AssembleActivePlugins extracts all active MCP servers, skills, and rules from enabled plugins.
	AssembleActivePlugins(ctx context.Context, globalHome string, workspaceDir string) (*domain.ResolvedContext, error)

	// SyncPlugins synchronizes and updates embedded builtin plugins to the specified target directory.
	SyncPlugins(ctx context.Context, destDir string, force bool) ([]domain.PluginSyncResult, error)

	// ListEmbeddedPlugins returns all plugins embedded in the binary.
	ListEmbeddedPlugins(ctx context.Context) ([]domain.Plugin, error)
}
