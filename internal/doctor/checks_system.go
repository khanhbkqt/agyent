package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// CheckSystem evaluates the host operating system, architecture, and directory permissions.
func (d *DoctorRunner) CheckSystem() []CheckResult {
	var results []CheckResult

	// 1. OS & Architecture
	results = append(results, CheckResult{
		Name:     "Host OS & Architecture",
		Category: CategorySystem,
		Status:   StatusPass,
		Message:  fmt.Sprintf("%s / %s (Go Runtime: %s, CPUs: %d)", runtime.GOOS, runtime.GOARCH, runtime.Version(), runtime.NumCPU()),
	})

	// 2. User Home Directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		results = append(results, CheckResult{
			Name:        "User Home Directory",
			Category:    CategorySystem,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Failed to resolve user home directory: %v", err),
			Remediation: "Ensure the USERPROFILE or HOME environment variable is properly set.",
		})
	} else {
		results = append(results, CheckResult{
			Name:     "User Home Directory",
			Category: CategorySystem,
			Status:   StatusPass,
			Message:  fmt.Sprintf("Resolved home directory: %s", homeDir),
		})
	}

	// 3. Temp Directory Writeability
	tempDir := os.TempDir()
	testFile := filepath.Join(tempDir, fmt.Sprintf("agyent_doctor_test_%d.tmp", os.Getpid()))
	if err := os.WriteFile(testFile, []byte("agyent_doctor_probe"), 0600); err != nil {
		results = append(results, CheckResult{
			Name:        "Temporary Directory Write Access",
			Category:    CategorySystem,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Cannot write to temp directory %s: %v", tempDir, err),
			Remediation: "Check OS file permissions and disk space in temporary folder.",
		})
	} else {
		_ = os.Remove(testFile)
		results = append(results, CheckResult{
			Name:     "Temporary Directory Write Access",
			Category: CategorySystem,
			Status:   StatusPass,
			Message:  fmt.Sprintf("Write access confirmed (%s)", tempDir),
		})
	}

	return results
}
