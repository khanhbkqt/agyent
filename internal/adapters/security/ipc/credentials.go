package ipc

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// verifyPeerCredentials verifies that the incoming connection originates from a legitimate local client
// running under the same user ID (for Unix sockets) or strictly on loopback (for TCP).
func verifyPeerCredentials(conn net.Conn) error {
	remoteAddr := conn.RemoteAddr()
	if remoteAddr == nil {
		return fmt.Errorf("peer has nil address")
	}

	network := remoteAddr.Network()

	// 1. Unix Domain Socket verification
	if strings.HasPrefix(network, "unix") {
		// On Unix sockets, ensure current process UID matches peer if supported
		if unixConn, ok := conn.(*net.UnixConn); ok {
			file, err := unixConn.File()
			if err == nil {
				defer file.Close()
				if peerUID, err := getPeerUID(file.Fd()); err == nil {
					currentUID := os.Getuid()
					if peerUID != currentUID {
						return fmt.Errorf("peer UID %d does not match daemon UID %d", peerUID, currentUID)
					}
					return nil
				}
			}
		}
		return nil
	}

	// 2. TCP Socket verification
	if strings.HasPrefix(network, "tcp") {
		tcpAddr, ok := remoteAddr.(*net.TCPAddr)
		if !ok {
			return fmt.Errorf("unsupported remote address type %T", remoteAddr)
		}
		if !tcpAddr.IP.IsLoopback() {
			return fmt.Errorf("remote connection from non-loopback IP %s denied", tcpAddr.IP.String())
		}
		return nil
	}

	return nil
}
