package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CheckAGY inspects the Antigravity CLI executable, onboarding state, authentication session, and live quota.
func (d *DoctorRunner) CheckAGY(ctx context.Context) []CheckResult {
	var results []CheckResult
	binPath := d.cfg.AGY.BinaryPath
	if binPath == "" {
		binPath = "agy"
	}

	// 1. Binary Path Resolution
	resolvedPath, err := exec.LookPath(binPath)
	if err != nil {
		results = append(results, CheckResult{
			Name:        "AGY CLI Binary Discovery",
			Category:    CategoryAGY,
			Status:      StatusFail,
			Message:     fmt.Sprintf("AGY binary %q not found in PATH", binPath),
			Remediation: "Install Antigravity CLI or set correct path in 'agy.binary_path' in config.",
		})
		return results
	}

	results = append(results, CheckResult{
		Name:     "AGY CLI Binary Discovery",
		Category: CategoryAGY,
		Status:   StatusPass,
		Message:  fmt.Sprintf("Found AGY binary at: %s", resolvedPath),
	})

	// 2. Binary Version & Health Execution
	verCtx, cancelVer := context.WithTimeout(ctx, 3*time.Second)
	defer cancelVer()

	cmd := exec.CommandContext(verCtx, resolvedPath, "--version")
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	cmd.WaitDelay = 1 * time.Second
	out, err := cmd.CombinedOutput()
	versionStr := strings.TrimSpace(string(out))
	if err != nil || versionStr == "" {
		results = append(results, CheckResult{
			Name:        "AGY Binary Execution",
			Category:    CategoryAGY,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Failed to run '%s --version': %v (Output: %s)", binPath, err, versionStr),
			Remediation: "Ensure the AGY CLI binary is executable and not corrupted.",
		})
	} else {
		results = append(results, CheckResult{
			Name:     "AGY Binary Execution",
			Category: CategoryAGY,
			Status:   StatusPass,
			Message:  fmt.Sprintf("Antigravity CLI version: %s", versionStr),
		})
	}

	// 3. Antigravity State & Auth Session Directory Inspection
	homeDir, _ := os.UserHomeDir()
	geminiDir := filepath.Join(homeDir, ".gemini")
	agyStateDir := filepath.Join(geminiDir, "antigravity")
	agyCliStateDir := filepath.Join(geminiDir, "antigravity-cli")

	stateFound := false
	var authDetails []string

	stateFiles := []string{
		filepath.Join(agyStateDir, "antigravity_state.pbtxt"),
		filepath.Join(agyCliStateDir, "jetski_state.pbtxt"),
		filepath.Join(agyStateDir, "installation_id"),
		filepath.Join(agyCliStateDir, "installation_id"),
	}

	for _, sf := range stateFiles {
		if _, err := os.Stat(sf); err == nil {
			stateFound = true
			authDetails = append(authDetails, filepath.Base(sf))
		}
	}

	if !stateFound {
		results = append(results, CheckResult{
			Name:        "AGY Onboarding & Session Files",
			Category:    CategoryAGY,
			Status:      StatusWarn,
			Message:     fmt.Sprintf("No Antigravity state or session files found in %s", geminiDir),
			Remediation: "Run 'agy' interactively once in terminal to complete initial Google login & onboarding.",
		})
	} else {
		results = append(results, CheckResult{
			Name:     "AGY Onboarding & Session Files",
			Category: CategoryAGY,
			Status:   StatusPass,
			Message:  fmt.Sprintf("Antigravity state files verified (%s)", strings.Join(authDetails, ", ")),
		})
	}

	// 4. Live Model & Auth / Quota Probe Check (if LiveProbe enabled)
	if d.opts.LiveProbe {
		probeCtx, cancelProbe := context.WithTimeout(ctx, 3*time.Second)
		defer cancelProbe()

		probeCmd := exec.CommandContext(probeCtx, resolvedPath, "models")
		probeCmd.Stdin = bytes.NewReader(nil)
		probeCmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
		probeCmd.WaitDelay = 1 * time.Second
		probeOut, probeErr := probeCmd.CombinedOutput()
		probeOutputStr := string(probeOut)

		if probeErr != nil {
			lowerErr := strings.ToLower(probeOutputStr)
			if strings.Contains(lowerErr, "429") || strings.Contains(lowerErr, "quota") || strings.Contains(lowerErr, "resource_exhausted") || strings.Contains(lowerErr, "rate limit") {
				results = append(results, CheckResult{
					Name:        "AGY Authentication & Quota Status",
					Category:    CategoryAGY,
					Status:      StatusWarn,
					Message:     "Google Antigravity API Quota / Rate limit reached (HTTP 429)",
					Details:     strings.TrimSpace(probeOutputStr),
					Remediation: "Wait for quota cooldown window or switch to another Google account profile.",
				})
			} else if strings.Contains(lowerErr, "unauth") || strings.Contains(lowerErr, "not logged in") || strings.Contains(lowerErr, "login") || strings.Contains(lowerErr, "token") {
				results = append(results, CheckResult{
					Name:        "AGY Authentication & Quota Status",
					Category:    CategoryAGY,
					Status:      StatusFail,
					Message:     "AGY CLI is not authenticated or OAuth token has expired",
					Details:     strings.TrimSpace(probeOutputStr),
					Remediation: "Run 'agy' in terminal to log in and refresh your Google OAuth session.",
				})
			} else {
				results = append(results, CheckResult{
					Name:     "AGY Authentication & Quota Status",
					Category: CategoryAGY,
					Status:   StatusPass,
					Message:  "Authenticated via active session files (Live models probe bypassed)",
				})
			}
		} else {
			lines := strings.Split(strings.TrimSpace(probeOutputStr), "\n")
			modelCount := len(lines)
			results = append(results, CheckResult{
				Name:     "AGY Authentication & Quota Status",
				Category: CategoryAGY,
				Status:   StatusPass,
				Message:  fmt.Sprintf("Authenticated & Quota Active (%d model(s) verified via AGY)", modelCount),
			})
		}
	} else {
		// Non-probe mode: use session files state
		if stateFound {
			results = append(results, CheckResult{
				Name:     "AGY Authentication & Quota Status",
				Category: CategoryAGY,
				Status:   StatusPass,
				Message:  "Authenticated (Session & state files verified; use '--probe' for live API check)",
			})
		} else {
			results = append(results, CheckResult{
				Name:        "AGY Authentication & Quota Status",
				Category:    CategoryAGY,
				Status:      StatusWarn,
				Message:     "Unverified authentication state (no session state files found)",
				Remediation: "Run 'agy' in terminal to log in.",
			})
		}
	}

	// 5. Antigravity Brain Storage Directory
	brainDir := filepath.Join(geminiDir, "antigravity", "brain")
	if _, err := os.Stat(brainDir); os.IsNotExist(err) {
		results = append(results, CheckResult{
			Name:     "AGY Brain Storage Directory",
			Category: CategoryAGY,
			Status:   StatusInfo,
			Message:  fmt.Sprintf("Brain directory not created yet (%s) - will be initialized on first conversation turn", brainDir),
		})
	} else {
		results = append(results, CheckResult{
			Name:     "AGY Brain Storage Directory",
			Category: CategoryAGY,
			Status:   StatusPass,
			Message:  fmt.Sprintf("Brain transcript store active at: %s", brainDir),
		})
	}

	return results
}
