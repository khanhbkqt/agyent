//go:build darwin

package ipc

import (
	"golang.org/x/sys/unix"
)

func getPeerUID(fd uintptr) (int, error) {
	xucred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return -1, err
	}
	return int(xucred.Uid), nil
}
