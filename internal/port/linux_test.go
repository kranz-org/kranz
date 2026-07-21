//go:build linux

package port

import "testing"

func TestParseSSOutputIncludesListenerMetadata(t *testing.T) {
	output := "LISTEN 0 4096 127.0.0.1:8080 0.0.0.0:* users:((\"api\",pid=4321,fd=3))\n"
	info := parseSSOutput(output, 8080, "tcp")
	if info == nil {
		t.Fatal("expected listener information")
	}
	if info.Address != "127.0.0.1" || info.Protocol != "tcp" || info.PID != 4321 {
		t.Fatalf("listener metadata = %#v", info)
	}
}
