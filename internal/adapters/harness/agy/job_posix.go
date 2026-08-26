//go:build !windows

package agy

import (
	"os"
)

// ProcessJobGuard is a no-op placeholder on POSIX platforms where PR_SET_PDEATHSIG handles cleanup.
type ProcessJobGuard struct{}

// CreateProcessJobGuard constructs a fallback ProcessJobGuard on Linux/macOS.
func CreateProcessJobGuard() (*ProcessJobGuard, error) {
	return &ProcessJobGuard{}, nil
}

// AttachProcess is a no-op on non-Windows systems.
func (g *ProcessJobGuard) AttachProcess(process *os.Process) error {
	return nil
}

// Close is a no-op on non-Windows systems.
func (g *ProcessJobGuard) Close() error {
	return nil
}
