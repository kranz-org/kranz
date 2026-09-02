package main

import (
	"sync/atomic"
	"testing"
	"time"
)

type recordingMouseEnabler struct{ calls atomic.Int32 }

func (e *recordingMouseEnabler) EnableMouseCellMotion() { e.calls.Add(1) }

func TestMouseWatchdogRecoversWithoutModelMessages(t *testing.T) {
	enabler := &recordingMouseEnabler{}
	ready := make(chan struct{})
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		runMouseWatchdog(enabler, mouseWatchdogConfig{
			interval: time.Millisecond,
			backstop: time.Hour,
			ready:    ready,
			done:     done,
		})
	}()
	time.Sleep(5 * time.Millisecond)
	if calls := enabler.calls.Load(); calls != 0 {
		t.Fatalf("mouse watchdog ran before renderer readiness: %d calls", calls)
	}
	close(ready)

	deadline := time.Now().Add(250 * time.Millisecond)
	for enabler.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if enabler.calls.Load() == 0 {
		t.Fatal("mouse watchdog did not re-enable mouse mode")
	}
	close(done)
	select {
	case <-exited:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("mouse watchdog did not stop")
	}
}

// A terminal that reports focus restores mouse mode through the focus handler,
// so the watchdog must stop polling four times a second once it sees one. The
// recovery guarantee stays for terminals that never report focus.
func TestMouseWatchdogRelaxesOnceTerminalReportsFocus(t *testing.T) {
	enabler := &recordingMouseEnabler{}
	ready := make(chan struct{})
	relaxed := make(chan struct{})
	done := make(chan struct{})
	defer close(done)
	go runMouseWatchdog(enabler, mouseWatchdogConfig{
		interval: time.Millisecond,
		backstop: time.Hour,
		ready:    ready,
		relaxed:  relaxed,
		done:     done,
	})
	close(ready)

	deadline := time.Now().Add(time.Second)
	for enabler.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if enabler.calls.Load() == 0 {
		t.Fatal("mouse watchdog did not re-enable mouse mode before focus was reported")
	}

	close(relaxed)
	time.Sleep(20 * time.Millisecond)
	settled := enabler.calls.Load()
	time.Sleep(50 * time.Millisecond)
	if grew := enabler.calls.Load() - settled; grew > 0 {
		t.Fatalf("watchdog kept polling after the terminal reported focus: %d more calls", grew)
	}
}
