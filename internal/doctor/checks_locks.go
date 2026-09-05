package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StaleLockRecord represents an orphaned lock file detected on disk.
type StaleLockRecord struct {
	Path        string
	Description string
}

// CheckStaleLocks inspects the environment for orphaned Antigravity CLI and browser profile lock files.
func (d *DoctorRunner) CheckStaleLocks() []CheckResult {
	var results []CheckResult

	locks := findStaleLockFiles()
	if len(locks) == 0 {
		results = append(results, CheckResult{
			Name:     "Stale File Lock Inspection",
			Category: CategoryStorage,
			Status:   StatusPass,
			Message:  "No stale session or browser profile locks detected.",
		})
		return results
	}

	var details []string
	for _, l := range locks {
		details = append(details, fmt.Sprintf("- %s: %s", l.Description, l.Path))
	}

	results = append(results, CheckResult{
		Name:        "Stale File Lock Inspection",
		Category:    CategoryStorage,
		Status:      StatusWarn,
		Message:     fmt.Sprintf("Found %d stale lock file(s) that may cause session hangs or profile contention", len(locks)),
		Details:     strings.Join(details, "\n"),
		Remediation: "Run 'agyent doctor --fix' to safely purge stale lock files.",
		CanAutoFix:  true,
	})

	return results
}

func findStaleLockFiles() []StaleLockRecord {
	var stale []StaleLockRecord
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return stale
	}

	// 1. Antigravity Presence Locks (~/.gemini/antigravity-cli/presence/*.lock & ~/.gemini/antigravity/presence/*.lock)
	// Janitor Safe Rule: Only purge locks older than 24 hours to prevent disrupting active CLI processes.
	presenceDirs := []string{
		filepath.Join(homeDir, ".gemini", "antigravity-cli", "presence"),
		filepath.Join(homeDir, ".gemini", "antigravity", "presence"),
	}

	for _, pDir := range presenceDirs {
		entries, err := os.ReadDir(pDir)
		if err == nil {
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".lock") {
					fullPath := filepath.Join(pDir, e.Name())
					info, err := e.Info()
					if err == nil {
						if time.Since(info.ModTime()) > 24*time.Hour {
							stale = append(stale, StaleLockRecord{
								Path:        fullPath,
								Description: fmt.Sprintf("AGY CLI Presence Lock (%d bytes, >24h old)", info.Size()),
							})
						}
					}
				}
			}
		}
	}

	// 2. Browser Camoufox Profile Locks (~/.agyent/camoufox/profiles/*/data_dir/parent.lock)
	// Janitor Safe Rule: Only purge profile locks older than 24 hours.
	camoufoxProfilesDir := filepath.Join(homeDir, ".agyent", "camoufox", "profiles")
	profEntries, err := os.ReadDir(camoufoxProfilesDir)
	if err == nil {
		for _, pe := range profEntries {
			if pe.IsDir() {
				lockCandidates := []string{
					filepath.Join(camoufoxProfilesDir, pe.Name(), "data_dir", "parent.lock"),
					filepath.Join(camoufoxProfilesDir, pe.Name(), "data_dir", ".parentlock"),
					filepath.Join(camoufoxProfilesDir, pe.Name(), "data_dir", "lock"),
				}
				for _, lc := range lockCandidates {
					if fi, err := os.Stat(lc); err == nil {
						if time.Since(fi.ModTime()) > 24*time.Hour {
							stale = append(stale, StaleLockRecord{
								Path:        lc,
								Description: fmt.Sprintf("Camoufox Profile Lock (%s, >24h old)", pe.Name()),
							})
						}
					}
				}
			}
		}
	}

	return stale
}
