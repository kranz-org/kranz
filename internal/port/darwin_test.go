//go:build darwin

package port

import (
	"reflect"
	"testing"
)

func TestParseLsofOutputIncludesListenerMetadata(t *testing.T) {
	info := parseLsofOutput("p4321\ncapi\nn127.0.0.1:8080\n", 8080, "tcp")
	if info == nil {
		t.Fatal("expected listener information")
	}
	if info.Address != "127.0.0.1" || info.Protocol != "tcp" || info.Port != 8080 {
		t.Fatalf("listener metadata = %#v", info)
	}
}

func TestParseLsofSnapshotFixtures(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []Listener
	}{
		{
			name: "ipv4 loopback and wildcard with several pids",
			output: "p101\ncapi\nf5\nPTCP\nn127.0.0.1:3000\n" +
				"p202\ncworker\nf7\nPTCP\nn*:8080\n",
			want: []Listener{
				{Protocol: "tcp", Address: "127.0.0.1", Port: 3000, PID: 101},
				{Protocol: "tcp", Address: "*", Port: 8080, PID: 202},
			},
		},
		{
			name:   "ipv6 loopback and wildcard",
			output: "p303\ncnode\nn[::1]:4000\nn[::]:4001\n",
			want: []Listener{
				{Protocol: "tcp", Address: "::1", Port: 4000, PID: 303},
				{Protocol: "tcp", Address: "::", Port: 4001, PID: 303},
			},
		},
		{name: "empty", output: "", want: nil},
		{
			name:   "malformed records are ignored",
			output: "pbad\nn127.0.0.1:abc\np404\nnmissing-port\nn127.0.0.1:9090\np\nn*:70000\n",
			want:   []Listener{{Protocol: "tcp", Address: "127.0.0.1", Port: 9090, PID: 404}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLsofSnapshot(tt.output); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("snapshot = %#v, want %#v", got, tt.want)
			}
		})
	}
}
