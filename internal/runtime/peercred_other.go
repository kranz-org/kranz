//go:build !darwin && !linux

package runtime

import "net"

// verifyPeerUser has no portable peer-credential syscall on this platform.
// The runtime directory's filesystem permissions (see socket.go) remain the
// primary defense; this is a narrower, best-effort second check that simply
// is not available here.
func verifyPeerUser(conn *net.UnixConn) error {
	return nil
}
