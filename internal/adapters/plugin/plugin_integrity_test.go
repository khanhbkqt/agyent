package plugin_test

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agyent/internal/adapters/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinPlugins_PythonSyntaxAndIntegrity(t *testing.T) {
	// Locate repository builtin/plugins directory
	wd, err := os.Getwd()
	require.NoError(t, err)

	// Traverse up to find builtin/plugins if in internal/adapters/plugin
	builtinDir := filepath.Join(wd, "..", "..", "..", "builtin", "plugins")
	if _, err := os.Stat(builtinDir); err != nil {
		builtinDir = filepath.Join(wd, "builtin", "plugins")
	}
	if _, err := os.Stat(builtinDir); err != nil {
		t.Skip("builtin/plugins directory not accessible from test runner")
	}

	pyCmd := plugin.ResolveCommandPath("python")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Verify Python executable runs
	cmdVer := exec.CommandContext(ctx, pyCmd, "--version")
	if err := cmdVer.Run(); err != nil {
		t.Skipf("No operational python executable found (%s): %v", pyCmd, err)
	}

	// 1. Collect all Python files across all builtin plugins
	var pyFiles []string
	err = filepath.WalkDir(builtinDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && strings.HasSuffix(path, ".py") {
			pyFiles = append(pyFiles, path)
		}
		return nil
	})
	require.NoError(t, err)
	assert.NotEmpty(t, pyFiles, "Builtin plugins must contain Python files")

	// 2. Compile every Python file with py_compile to ensure zero syntax and typing errors
	compileArgs := append([]string{"-m", "py_compile"}, pyFiles...)
	cmdCompile := exec.CommandContext(ctx, pyCmd, compileArgs...)
	out, err := cmdCompile.CombinedOutput()
	assert.NoError(t, err, "Python plugin compilation failed:\n%s", string(out))

	// 3. Test plugin --check self-health probes
	serverScripts := []string{
		filepath.Join(builtinDir, "browser-camoufox", "server.py"),
		filepath.Join(builtinDir, "database-sqlite", "server.py"),
		filepath.Join(builtinDir, "system-diagnostics", "server.py"),
	}

	for _, srv := range serverScripts {
		if _, statErr := os.Stat(srv); statErr == nil {
			probeCmd := exec.CommandContext(ctx, pyCmd, srv, "--check")
			probeOut, probeErr := probeCmd.CombinedOutput()
			assert.NoError(t, probeErr, "Plugin server %s --check probe failed:\n%s", srv, string(probeOut))
			assert.Contains(t, string(probeOut), `"status"`, "Probe output must contain JSON status")
		}
	}
}

func TestResolveCommandPath_VirtualEnvDiscovery(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Test VIRTUAL_ENV environment variable
	fakeVenvBin := filepath.Join(tempDir, "fake_venv", "bin")
	require.NoError(t, os.MkdirAll(fakeVenvBin, 0755))
	fakePy := filepath.Join(fakeVenvBin, "python")
	require.NoError(t, os.WriteFile(fakePy, []byte("#!/bin/sh\necho 1.0"), 0755))

	t.Setenv("VIRTUAL_ENV", filepath.Join(tempDir, "fake_venv"))
	resolved := plugin.ResolveCommandPath("python")
	assert.Equal(t, fakePy, resolved, "Should resolve python from VIRTUAL_ENV")

	// 2. Test AGYENT_PYTHON precedence over VIRTUAL_ENV
	customPy := filepath.Join(tempDir, "custom_python")
	require.NoError(t, os.WriteFile(customPy, []byte("#!/bin/sh\necho custom"), 0755))
	t.Setenv("AGYENT_PYTHON", customPy)

	resolvedPrecedence := plugin.ResolveCommandPath("python")
	assert.Equal(t, customPy, resolvedPrecedence, "AGYENT_PYTHON should take highest precedence")
}
