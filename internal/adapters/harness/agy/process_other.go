//go:build !linux && !windows

package agy

import "syscall"

func setProcDeathSig(attr *syscall.SysProcAttr) {
}
