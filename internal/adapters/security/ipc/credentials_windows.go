//go:build windows

package ipc

func getPeerUID(fd uintptr) (int, error) {
	// On Windows, loopback / named pipe security is handled by Windows ACLs
	return 0, nil
}
