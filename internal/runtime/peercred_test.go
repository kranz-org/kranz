package runtime

import (
	"net"
	"testing"
)

// TestVerifyPeerUserAcceptsOurOwnConnection is a same-process, same-user
// sanity check: it cannot fabricate a different-UID peer without root or a
// container, but it does prove the syscall path itself works end to end on
// this platform, rather than only compiling.
func TestVerifyPeerUserAcceptsOurOwnConnection(t *testing.T) {
	_, socketPath, cleanup, err := NewSocketDir()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	listener, err := listenUnix(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			accepted <- err
			return
		}
		defer func() { _ = conn.Close() }()
		accepted <- verifyPeerUser(conn)
	}()

	client, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	if err := <-accepted; err != nil {
		t.Fatalf("verifyPeerUser rejected our own connection: %v", err)
	}
}
