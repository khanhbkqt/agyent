//go:build !darwin && !linux && !windows

package ipc

func getPeerUID(fd uintptr) (int, error) {
	return 0, nil
}
