package doctor

import (
	"fmt"
	"os"
	"path/filepath"

	"agyent/internal/config"
)

// CheckSecurity inspects the Universal Security Gateway configuration, workspace hooks, and IPC host.
func (d *DoctorRunner) CheckSecurity() []CheckResult {
	var results []CheckResult

	sec := d.cfg.Security
	// 1. Security Preset Check
	preset := sec.Preset
	if preset == "" {
		preset = "balanced"
	}

	results = append(results, CheckResult{
		Name:     "Security Gateway Preset",
		Category: CategorySecurity,
		Status:   StatusPass,
		Message:  fmt.Sprintf("Preset: %q (Mode: %s, HITL Timeout: %ds, DLP: %t)", preset, sec.Mode, sec.ApprovalTimeoutSeconds, sec.DLP.Enabled),
	})

	// 2. Starter Workspace Security Hooks Provisioning Check
	agentsDir := d.cfg.Storage.AgentsDir
	if agentsDir != "" {
		starterWS := config.ResolveAgentWorkspace(agentsDir, "")
		hooksPath := filepath.Join(starterWS, ".agents", "hooks.json")
		if _, err := os.Stat(hooksPath); os.IsNotExist(err) {
			results = append(results, CheckResult{
				Name:        "Workspace Security Hooks",
				Category:    CategorySecurity,
				Status:      StatusWarn,
				Message:     fmt.Sprintf("Security hooks not found at %s (will be provisioned on daemon start)", hooksPath),
				Remediation: "Run 'agyent doctor --fix' or start daemon with 'agyent run'.",
				CanAutoFix:  true,
			})
		} else {
			results = append(results, CheckResult{
				Name:     "Workspace Security Hooks",
				Category: CategorySecurity,
				Status:   StatusPass,
				Message:  fmt.Sprintf("Hooks provisioned at: %s", hooksPath),
			})
		}
	}

	return results
}
