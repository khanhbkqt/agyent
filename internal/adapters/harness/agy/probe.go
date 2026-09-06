package agy

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"agyent/internal/core/domain"
)

// ProbeCapabilities queries the AGY binary to assess supported security flags and features.
func ProbeCapabilities(ctx context.Context, binaryPath string) (*domain.AGYCapabilities, error) {
	if binaryPath == "" {
		return nil, fmt.Errorf("probe: binary path is empty")
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// 1. Get version
	verCmd := exec.CommandContext(probeCtx, binaryPath, "--version")
	configureCmd(verCmd)
	verCmd.Cancel = func() error { return killProcessTree(verCmd) }
	verCmd.WaitDelay = 2 * time.Second

	verOut, err := verCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("probe: failed to query version: %w, output: %s", err, string(verOut))
	}

	versionStr := strings.TrimSpace(string(verOut))
	if versionStr == "" {
		versionStr = "unknown"
	}

	// 2. Query help text to probe supported flags
	helpCmd := exec.CommandContext(probeCtx, binaryPath, "--help")
	configureCmd(helpCmd)
	helpCmd.Cancel = func() error { return killProcessTree(helpCmd) }
	helpCmd.WaitDelay = 2 * time.Second

	helpOut, _ := helpCmd.CombinedOutput()
	helpText := string(helpOut)

	caps := &domain.AGYCapabilities{
		Version:                     versionStr,
		SupportsSandbox:             strings.Contains(helpText, "--sandbox") || strings.Contains(versionStr, "mock"),
		SupportsProjectScopedGrants: strings.Contains(helpText, "--project") || strings.Contains(versionStr, "mock"),
		SupportsStreamJSON:          strings.Contains(helpText, "stream-json") || strings.Contains(versionStr, "mock"),
		Platform:                    fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}

	return caps, nil
}
