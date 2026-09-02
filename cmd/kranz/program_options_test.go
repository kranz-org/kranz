package main

import "testing"

// Every dashboard is an attached client and must share one terminal contract,
// regardless of whether it started or discovered the independent runtime.
func TestBothDashboardsShareProgramOptions(t *testing.T) {
	if got := len(dashboardProgramOptions()); got != 4 {
		t.Fatalf("dashboard program options = %d, want alt screen, mouse, focus and FPS", got)
	}
}

// The renderer ticks whether or not anything changed, so this cap is what keeps
// an idle dashboard from repainting sixty times a second.
func TestDashboardCapsRepaintRate(t *testing.T) {
	if dashboardFPS <= 0 || dashboardFPS > 30 {
		t.Fatalf("dashboardFPS = %d, want a cap no higher than 30", dashboardFPS)
	}
}
