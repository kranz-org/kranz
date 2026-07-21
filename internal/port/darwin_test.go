//go:build darwin

package port

import "testing"

func TestParseLsofOutputIncludesListenerMetadata(t *testing.T) {
	info := parseLsofOutput("p4321\ncapi\nn127.0.0.1:8080\n", 8080, "tcp")
	if info == nil {
		t.Fatal("expected listener information")
	}
	if info.Address != "127.0.0.1" || info.Protocol != "tcp" || info.Port != 8080 {
		t.Fatalf("listener metadata = %#v", info)
	}
}
