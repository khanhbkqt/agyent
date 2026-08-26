package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"agyent/internal/core/domain"
)

// DependencyCheckResult contains pre-flight validation status for an executable dependency.
type DependencyCheckResult struct {
	BinaryName string
	Available  bool
	Path       string
	Error      string
}

// ValidatePluginDependencies inspects all MCP server command declarations within a plugin
// to verify they are resolvable via exec.LookPath before execution.
func ValidatePluginDependencies(ctx context.Context, p *domain.Plugin) []DependencyCheckResult {
	var results []DependencyCheckResult

	for _, srv := range p.MCPServers {
		if srv.Command == "" {
			continue
		}
		path, err := exec.LookPath(srv.Command)
		res := DependencyCheckResult{
			BinaryName: srv.Command,
			Available:  err == nil,
			Path:       path,
		}
		if err != nil {
			res.Error = fmt.Sprintf("Executable %q not found in PATH for plugin %q", srv.Command, p.Manifest.Name)
		}
		results = append(results, res)
	}

	return results
}

// StrictDecodeManifest decodes a plugin.json file rejecting any unrecognized fields for security.
func StrictDecodeManifest(data []byte) (*domain.PluginManifest, error) {
	var manifest domain.PluginManifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("malformed or unapproved plugin manifest schema: %w", err)
	}
	if manifest.Name == "" {
		return nil, fmt.Errorf("plugin manifest missing mandatory 'name' field")
	}
	return &manifest, nil
}
