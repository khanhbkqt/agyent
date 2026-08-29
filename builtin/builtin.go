package builtin

import "embed"

// EmbeddedPluginsFS embeds the entire builtin/plugins directory tree at compile time.
// This ensures that the single static agyent binary carries all capability plugins
// without requiring local git repository files or network downloads.
//
//go:embed all:plugins
var EmbeddedPluginsFS embed.FS
