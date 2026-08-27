//go:build windows

package agy

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const (
	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
)

func configureCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | createNoWindow,
		HideWindow:    true,
	}
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil
	}

	pid := strconv.Itoa(cmd.Process.Pid)

	// Context timeout 2s ensures taskkill never blocks indefinitely
	killCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	killCmd := exec.CommandContext(killCtx, "taskkill", "/F", "/T", "/PID", pid)
	killCmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}

	_ = killCmd.Run()
	_ = cmd.Process.Kill()
	return nil
}

// ConfigureCmd configures OS subprocess flags.
func ConfigureCmd(cmd *exec.Cmd) {
	configureCmd(cmd)
}

// KillProcessTree terminates the child process and all its descendants.
func KillProcessTree(cmd *exec.Cmd) error {
	return killProcessTree(cmd)
}
