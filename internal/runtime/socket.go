package runtime

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// NewSocketDir creates a fresh, owner-only runtime directory under the OS
// temporary directory and returns the Unix socket path inside it. This
// stream has no session registry yet (that is поток 4's job — stable names,
// IDs, and a well-known runtime path), so every run gets its own throwaway,
// randomly named directory: nothing here is meant to be discovered by a
// second process yet, only to prove the socket protocol end to end.
func NewSocketDir() (dir, socketPath string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "kranz-runtime-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("create runtime directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, fmt.Errorf("restrict runtime directory: %w", err)
	}
	socketPath = filepath.Join(dir, "kranz.sock")
	cleanup = func() { _ = os.RemoveAll(dir) }
	return dir, socketPath, cleanup, nil
}

// listenUnix binds socketPath and restricts it to the owner. net.Listen
// creates the socket file with permissions shaped by umask, which is not a
// guarantee; the explicit chmod after Listen is the actual contract.
func listenUnix(socketPath string) (*net.UnixListener, error) {
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		_ = listener.Close()
		return nil, fmt.Errorf("listener for %s was not a Unix socket listener", socketPath)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = unixListener.Close()
		return nil, fmt.Errorf("restrict socket %s: %w", socketPath, err)
	}
	return unixListener, nil
}
