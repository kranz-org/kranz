package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// A focus probe must not arm a second backstop tick. Otherwise every window
// switch would leave another timer behind, and the polling this change exists
// to slow down would speed back up.
func TestFocusProbeDoesNotArmAnotherTick(t *testing.T) {
	model := newTestModel()
	model.systemAppearanceSet = true

	_, cmd := model.Update(systemAppearanceMsg{available: true, scheduled: true})
	if cmd == nil {
		t.Fatal("the backstop tick must re-arm itself")
	}

	_, cmd = model.Update(systemAppearanceMsg{available: true})
	if cmd != nil {
		t.Fatal("a focus probe must not arm a tick")
	}
}

// Regaining focus is the moment the user comes back from changing the OS theme,
// so it has to trigger a read rather than wait for the backstop.
func TestFocusProbesSystemAppearance(t *testing.T) {
	model := newTestModel()
	model.lastAppearanceProbe = time.Time{}

	if cmd := model.probeSystemAppearance(); cmd == nil {
		t.Fatal("regained focus must probe the system appearance")
	}
	if cmd := model.probeSystemAppearance(); cmd != nil {
		t.Fatal("a burst of focus events must be debounced into one probe")
	}
}

// The focus handler keeps re-asserting mouse tracking; the appearance probe
// must ride along with it rather than replace it.
func TestFocusStillRestoresMouseTracking(t *testing.T) {
	model := newTestModel()
	_, cmd := model.Update(tea.FocusMsg{})
	if cmd == nil {
		t.Fatal("focus must still issue commands")
	}
}
