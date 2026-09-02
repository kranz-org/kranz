package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/port"
)

// steadyListenerScanner always reports the same listeners, which is what a
// project looks like once its services have finished starting.
type steadyListenerScanner struct{ calls atomic.Int32 }

func (s *steadyListenerScanner) Snapshot(context.Context) ([]port.Listener, error) {
	s.calls.Add(1)
	return nil, nil
}

// Taking a listener snapshot shells out to lsof or ss and costs tens of
// milliseconds of CPU. A project whose ports have stopped changing must stop
// paying that on a fixed clock.
func TestQuietProjectBacksOffListenerScanning(t *testing.T) {
	manager := newSleepingDiscoveryManager(t)
	manager.listenerScanInterval = 10 * time.Millisecond
	scanner := &steadyListenerScanner{}
	manager.SetListenerScanner(scanner)
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })

	waitForCondition(t, func() bool { return scanner.calls.Load() > 0 }, "first listener snapshot")
	time.Sleep(400 * time.Millisecond)
	// A fixed 10 ms cadence would be about forty snapshots over this window.
	if calls := scanner.calls.Load(); calls > 12 {
		t.Fatalf("quiet project kept scanning listeners: %d snapshots in 400ms", calls)
	}
}

// Backing off must not cost responsiveness where it matters: a service that
// just started is about to open its ports, and they have to show up promptly.
func TestServiceStartRestoresListenerScanCadence(t *testing.T) {
	manager := newSleepingDiscoveryManager(t)
	manager.listenerScanInterval = 10 * time.Millisecond
	scanner := &steadyListenerScanner{}
	manager.SetListenerScanner(scanner)
	if err := manager.StartService("api"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })

	waitForCondition(t, func() bool { return scanner.calls.Load() > 0 }, "first listener snapshot")
	time.Sleep(400 * time.Millisecond)
	settled := scanner.calls.Load()

	// This is the path StartService takes for an already running discovery loop.
	manager.ensureListenerDiscovery()
	waitForCondition(t, func() bool { return scanner.calls.Load() > settled }, "rescan after a service start")
}
