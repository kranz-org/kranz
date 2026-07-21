package port

import (
	"os"
	"strings"
	"testing"
)

func TestListenerAddress(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1:8080": "127.0.0.1",
		"*:8080":         "*",
		"[::1]:8080":     "::1",
		"[::]:8080":      "::",
	}
	for endpoint, expected := range tests {
		if actual := listenerAddress(endpoint); actual != expected {
			t.Errorf("listenerAddress(%q) = %q, want %q", endpoint, actual, expected)
		}
	}
}

func TestTerminateExternalPIDRejectsProtectedProcesses(t *testing.T) {
	for _, pid := range []int{0, 1, os.Getpid()} {
		if err := TerminateExternalPID(pid, 0); err == nil {
			t.Fatalf("TerminateExternalPID(%d) unexpectedly succeeded", pid)
		} else if pid == os.Getpid() && !strings.Contains(err.Error(), "Kranz") {
			t.Fatalf("self-protection error = %v", err)
		}
	}
}
