//go:build !windows

package agy

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureCmd(cmd *exec.Cmd) {
	attr := &syscall.SysProcAttr{
		Setpgid: true,
	}
	setProcDeathSig(attr)
	cmd.SysProcAttr = attr
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil
	}

	pgid := cmd.Process.Pid
	err := syscall.Kill(-pgid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = cmd.Process.Kill()
		return err
	}
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
