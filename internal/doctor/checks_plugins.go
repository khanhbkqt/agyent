package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agyent/internal/adapters/plugin"
)

// CheckPlugins inspects capability plugins, Python runtime, Camoufox engine, and MCP configuration files.
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

	// 3. Check Python Runtime for Plugins & MCP Servers
	pyCmd := plugin.ResolveCommandPath("python")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmdVer := exec.CommandContext(ctx, pyCmd, "--version")
	verOut, verErr := cmdVer.CombinedOutput()
	pythonUsable := false

	if verErr != nil {
		results = append(results, CheckResult{
			Name:        "Python Runtime for Plugins",
			Category:    CategoryPlugins,
			Status:      StatusFail,
			Message:     fmt.Sprintf("No operational Python runtime found (resolved '%s')", pyCmd),
			Details:     fmt.Sprintf("Failed to run '%s --version': %v", pyCmd, verErr),
			Remediation: "Install Python 3.10+ or set AGYENT_PYTHON=/path/to/python (or create a virtualenv at ~/.agyent/venv)",
		})
	} else {
		pythonUsable = true
		results = append(results, CheckResult{
			Name:     "Python Runtime for Plugins",
			Category: CategoryPlugins,
			Status:   StatusPass,
			Message:  fmt.Sprintf("Operational runtime: %s (%s)", pyCmd, strings.TrimSpace(string(verOut))),
		})
	}

	// 4. Check Camoufox Stealth Engine & Plugin Dependencies
	camoufoxServerPath := filepath.Join(pluginsDir, "browser-camoufox", "server.py")
	if _, err := os.Stat(camoufoxServerPath); err == nil && pythonUsable {
		ctxCam, cancelCam := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelCam()

		cmdCheck := exec.CommandContext(ctxCam, pyCmd, camoufoxServerPath, "--check")
		outCheck, errCheck := cmdCheck.CombinedOutput()

		type camoufoxCheckResult struct {
			Status            string `json:"status"`
			CamoufoxAvailable bool   `json:"camoufox_available"`
			ImportError       string `json:"import_error"`
			Python            string `json:"python"`
			Version           string `json:"version"`
			ToolsCount        int    `json:"tools_count"`
		}

		var checkRes camoufoxCheckResult
		if parseErr := json.Unmarshal(bytes.TrimSpace(outCheck), &checkRes); parseErr == nil && checkRes.CamoufoxAvailable {
			results = append(results, CheckResult{
				Name:     "Camoufox Stealth Engine & Dependencies",
				Category: CategoryPlugins,
				Status:   StatusPass,
				Message:  fmt.Sprintf("Camoufox engine ready (v%s, %d tools verified in %s)", checkRes.Version, checkRes.ToolsCount, checkRes.Python),
			})
		} else {
			errMsg := "Dependencies missing or import error"
			if checkRes.ImportError != "" {
				errMsg = checkRes.ImportError
			} else if errCheck != nil {
				errMsg = fmt.Sprintf("%v (%s)", errCheck, strings.TrimSpace(string(outCheck)))
			}
			results = append(results, CheckResult{
				Name:        "Camoufox Stealth Engine & Dependencies",
				Category:    CategoryPlugins,
				Status:      StatusWarn,
				Message:     fmt.Sprintf("Camoufox dependencies not fully available: %s", errMsg),
				Remediation: fmt.Sprintf("Run: %s -m pip install \"camoufox[geoip]\" playwright && %s -m camoufox fetch", pyCmd, pyCmd),
			})
		}
	}

	// 5. Check Python Plugin Syntax & Typing Integrity
	if pythonUsable && err == nil {
		var pyFiles []string
		_ = filepath.WalkDir(pluginsDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr == nil && !d.IsDir() && strings.HasSuffix(path, ".py") {
				pyFiles = append(pyFiles, path)
			}
			return nil
		})

		if len(pyFiles) > 0 {
			ctxCompile, cancelCompile := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelCompile()

			compileArgs := append([]string{"-m", "py_compile"}, pyFiles...)
			cmdCompile := exec.CommandContext(ctxCompile, pyCmd, compileArgs...)
			compOut, compErr := cmdCompile.CombinedOutput()

			if compErr != nil {
				results = append(results, CheckResult{
					Name:        "Plugin Python Syntax & Typing Integrity",
					Category:    CategoryPlugins,
					Status:      StatusFail,
					Message:     "Syntax or typing definition compilation errors detected in plugin files",
					Details:     strings.TrimSpace(string(compOut)),
					Remediation: "Verify missing imports (e.g., typing) and correct syntax errors in reported files",
				})
			} else {
				results = append(results, CheckResult{
					Name:     "Plugin Python Syntax & Typing Integrity",
					Category: CategoryPlugins,
					Status:   StatusPass,
					Message:  fmt.Sprintf("%d Python plugin file(s) compiled with zero syntax or typing errors", len(pyFiles)),
				})
			}
		}
	}

	return results
}
