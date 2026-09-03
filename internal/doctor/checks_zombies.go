package doctor

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ProcessRecord holds minimal process telemetry for orphan analysis.
type ProcessRecord struct {
	PID        int
	PPID       int
	ElapsedSec int
	Command    string
	FullArgs   string
}

// CheckZombies inspects running processes for abandoned browser daemons, playwright runners, or zombie workers.
func (d *DoctorRunner) CheckZombies(ctx context.Context) []CheckResult {
	var results []CheckResult

	zombies, err := findZombieProcesses(ctx)
	if err != nil {
		results = append(results, CheckResult{
			Name:     "Zombie & Orphaned Process Health",
			Category: CategorySystem,
			Status:   StatusInfo,
			Message:  fmt.Sprintf("Could not inspect OS process table: %v", err),
		})
		return results
	}

	if len(zombies) == 0 {
		results = append(results, CheckResult{
			Name:     "Zombie & Orphaned Process Health",
			Category: CategorySystem,
			Status:   StatusPass,
			Message:  "No zombie browser or orphaned worker processes detected.",
		})
		return results
	}

	var details []string
	for _, z := range zombies {
		details = append(details, fmt.Sprintf("PID %d (PPID %d, running %ds): %s", z.PID, z.PPID, z.ElapsedSec, z.Command))
	}

	results = append(results, CheckResult{
		Name:        "Zombie & Orphaned Process Health",
		Category:    CategorySystem,
		Status:      StatusWarn,
		Message:     fmt.Sprintf("Detected %d orphaned/zombie process(es) holding system resources", len(zombies)),
		Details:     strings.Join(details, "\n"),
		Remediation: "Run 'agyent doctor --fix' to safely terminate orphaned background processes.",
		CanAutoFix:  true,
	})

	return results
}

// findZombieProcesses discovers leftover camoufox, playwright driver, or orphaned workers.
func findZombieProcesses(ctx context.Context) ([]ProcessRecord, error) {
	if runtime.GOOS == "windows" {
		return findZombiesWindows(ctx)
	}
	return findZombiesUnix(ctx)
}

func findZombiesUnix(ctx context.Context) ([]ProcessRecord, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "ps", "-eo", "pid,ppid,etimes,comm,args")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var zombies []ProcessRecord
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		etimes, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue // Header line or invalid format
		}

		comm := fields[3]
		fullArgs := strings.Join(fields[4:], " ")

		isZombie := false

		// 1. Stale Camoufox browser running headless for > 15 minutes
		if strings.Contains(comm, "camoufox") || strings.Contains(fullArgs, "camoufox-bin") {
			if etimes > 900 {
				isZombie = true
			}
		}

		// 2. Stale Playwright node driver running for > 15 minutes
		if strings.Contains(fullArgs, "playwright/driver") && etimes > 900 {
			isZombie = true
		}

		// 3. Stale Camoufox daemon running without agyent parent
		if strings.Contains(fullArgs, "browser-camoufox/daemon.py") && etimes > 1800 {
			isZombie = true
		}

		if isZombie {
			zombies = append(zombies, ProcessRecord{
				PID:        pid,
				PPID:       ppid,
				ElapsedSec: etimes,
				Command:    comm,
				FullArgs:   fullArgs,
			})
		}
	}

	return zombies, nil
}

func findZombiesWindows(ctx context.Context) ([]ProcessRecord, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "tasklist", "/fo", "csv", "/nh")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var zombies []ProcessRecord
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\",\"")
		if len(parts) < 2 {
			continue
		}
		procName := strings.Trim(parts[0], "\"")
		pidStr := strings.Trim(parts[1], "\"")
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		// Windows tasklist matching
		if strings.Contains(strings.ToLower(procName), "camoufox") {
			zombies = append(zombies, ProcessRecord{
				PID:     pid,
				Command: procName,
			})
		}
	}

	return zombies, nil
}
