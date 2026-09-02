package ui

import (
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// completeScan stands in for the inspection command finishing.
func completeScan(model *Model) {
	svc := model.FocusedService()
	model.portScanBusy = false
	model.portService = svc.Name
	model.portChecked = time.Now()
}

// Inspecting ports shells out to lsof. A dashboard nobody is touching must not
// pay for that on a timer.
func TestQuietDashboardDoesNotRescanPorts(t *testing.T) {
	model := newTestModel()
	model.refreshServices()
	if cmd := model.scanFocusedPorts(true); cmd == nil {
		t.Fatal("a forced scan must inspect ports")
	}
	completeScan(model)

	if cmd := model.scanFocusedPorts(false); cmd != nil {
		t.Fatal("an unchanged focused service must not be rescanned")
	}
}

// The reason to rescan is that the answer changed, and a service starting or
// stopping is exactly when it does. That must not wait for the backstop.
func TestServiceStateChangeRescansPortsImmediately(t *testing.T) {
	model := newTestModel()
	model.refreshServices()
	_ = model.scanFocusedPorts(true)
	completeScan(model)

	svc := model.FocusedService()
	svc.State.Status = config.StatusRunning
	svc.State.PID = 4242
	if cmd := model.scanFocusedPorts(false); cmd == nil {
		t.Fatal("a service that just started must have its ports rescanned")
	}
	completeScan(model)

	svc.State.PID = 4243
	if cmd := model.scanFocusedPorts(false); cmd == nil {
		t.Fatal("a restarted service holds its port under a new PID; rescan")
	}
}
