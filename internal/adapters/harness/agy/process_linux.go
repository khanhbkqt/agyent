//go:build linux

package agy

import "syscall"

func setProcDeathSig(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGKILL
}
