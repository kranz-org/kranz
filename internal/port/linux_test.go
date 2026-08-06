//go:build linux

package port

import (
	"reflect"
	"testing"
)

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

func TestParseSSSnapshotFixtures(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []Listener
	}{
		{
			name: "ipv4 loopback and wildcard",
			output: "LISTEN 0 4096 127.0.0.1:3000 0.0.0.0:* users:((\"api\",pid=101,fd=3))\n" +
				"LISTEN 0 128 *:8080 *:* users:((\"worker\",pid=202,fd=7))\n",
			want: []Listener{
				{Protocol: "tcp", Address: "127.0.0.1", Port: 3000, PID: 101},
				{Protocol: "tcp", Address: "*", Port: 8080, PID: 202},
			},
		},
		{
			name: "ipv6 and several users tuples",
			output: "LISTEN 0 511 [::1]:4000 [::]:* users:((\"node\",pid=303,fd=8),(\"node\",pid=304,fd=9))\n" +
				"LISTEN 0 511 [::]:4001 [::]:* users:((\"node\",pid=303,fd=10))\n",
			want: []Listener{
				{Protocol: "tcp", Address: "::1", Port: 4000, PID: 303},
				{Protocol: "tcp", Address: "::1", Port: 4000, PID: 304},
				{Protocol: "tcp", Address: "::", Port: 4001, PID: 303},
			},
		},
		{name: "empty", output: "", want: nil},
		{
			name: "malformed and permission-limited rows are ignored",
			output: "garbage\n" +
				"LISTEN 0 4096 127.0.0.1:abc 0.0.0.0:* users:((\"api\",pid=101,fd=3))\n" +
				"LISTEN 0 4096 127.0.0.1:9090 0.0.0.0:*\n" +
				"LISTEN 0 4096 127.0.0.1:70000 0.0.0.0:* users:((\"api\",pid=101,fd=3))\n",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseSSSnapshot(tt.output); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("snapshot = %#v, want %#v", got, tt.want)
			}
		})
	}
}
