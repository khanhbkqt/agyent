package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckPlugins inspects capability plugins and MCP configuration files.
func (d *DoctorRunner) CheckPlugins() []CheckResult {
	var results []CheckResult

	// 1. Check Builtin Plugins Directory
	pluginsDir := "builtin/plugins"
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		results = append(results, CheckResult{
			Name:     "Builtin Capability Plugins",
			Category: CategoryPlugins,
			Status:   StatusInfo,
			Message:  fmt.Sprintf("Plugins directory %s not present or empty", pluginsDir),
		})
	} else {
		var pluginNames []string
		for _, e := range entries {
			if e.IsDir() {
				pluginNames = append(pluginNames, e.Name())
			}
		}
		results = append(results, CheckResult{
			Name:     "Builtin Capability Plugins",
			Category: CategoryPlugins,
			Status:   StatusPass,
			Message:  fmt.Sprintf("%d plugin(s) found [%s]", len(pluginNames), strings.Join(pluginNames, ", ")),
		})
	}

	// 2. Check MCP Syncer Configuration File
	homeDir, _ := os.UserHomeDir()
	mcpPaths := []string{
		filepath.Join(homeDir, ".gemini", "antigravity", "mcp_config.json"),
		filepath.Join(homeDir, ".gemini", "config", "mcp_config.json"),
	}

	mcpFound := false
	var foundPath string
	for _, p := range mcpPaths {
		if _, err := os.Stat(p); err == nil {
			mcpFound = true
			foundPath = p
			break
		}
	}

	if mcpFound {
		results = append(results, CheckResult{
			Name:     "Model Context Protocol (MCP) Config",
			Category: CategoryPlugins,
			Status:   StatusPass,
			Message:  fmt.Sprintf("Active MCP configuration: %s", foundPath),
		})
	} else {
		results = append(results, CheckResult{
			Name:     "Model Context Protocol (MCP) Config",
			Category: CategoryPlugins,
			Status:   StatusInfo,
			Message:  "No global MCP config found (will be generated dynamically if MCP servers are declared)",
		})
	}

	return results
}
