package health

import (
	"fmt"
	"net"
	"time"
)

// checkTCP succeeds when a TCP connection can be established before the timeout.
func checkTCP(port int, timeout time.Duration) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("TCP connect %s: %w", addr, err)
	}
	conn.Close()
	return nil
}
