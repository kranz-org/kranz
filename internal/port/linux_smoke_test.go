//go:build linux

package port

import (
	"context"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestLinuxListenerScannerSmoke(t *testing.T) {
	if _, err := exec.LookPath("ss"); err != nil {
		t.Skip("ss is not installed")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	wantPort := listener.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listeners, err := (&LinuxChecker{}).Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	for _, observed := range listeners {
		if observed.Protocol == "tcp" && observed.Port == wantPort && observed.PID == os.Getpid() {
			return
		}
	}
	t.Fatalf("real listener port=%d pid=%d missing from snapshot: %#v", wantPort, os.Getpid(), listeners)
}
