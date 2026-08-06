package port

import (
	"context"
	"testing"
)

type listenerScannerFixture struct {
	listeners []Listener
}

func (f listenerScannerFixture) Snapshot(context.Context) ([]Listener, error) {
	return f.listeners, nil
}

func TestListenerScannerUsesMinimalSnapshotModel(t *testing.T) {
	var scanner ListenerScanner = listenerScannerFixture{listeners: []Listener{{
		Protocol: "tcp",
		Address:  "127.0.0.1",
		Port:     8080,
		PID:      4321,
	}}}

	listeners, err := scanner.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listeners) != 1 || listeners[0].Port != 8080 || listeners[0].PID != 4321 {
		t.Fatalf("snapshot = %#v", listeners)
	}
}
