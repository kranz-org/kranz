//go:build darwin

package runtime

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// verifyPeerUser confirms conn's remote end belongs to the same OS user as
// this process. Kranz's runtime directory is already restricted to the owner
// by filesystem permissions (see socket.go); this check defends the socket
// itself against another local user who can still connect to a Unix socket
// they can see, even one they cannot read the metadata for.
func verifyPeerUser(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("inspect peer connection: %w", err)
	}
	var cred *unix.Xucred
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return fmt.Errorf("inspect peer connection: %w", err)
	}
	if sockErr != nil {
		return fmt.Errorf("read peer credentials: %w", sockErr)
	}
	if uid := os.Getuid(); int(cred.Uid) != uid {
		return fmt.Errorf("peer uid %d does not match runtime owner %d", cred.Uid, uid)
	}
	return nil
}
