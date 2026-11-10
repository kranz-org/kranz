package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestModalWidthBoundsPreserveLongContent(t *testing.T) {
	// Include ANSI styling, double-width characters and a combining character.
	text := strings.Repeat("service-界-e\u0301/", 32)
	styled := "\x1b[1;31m" + text + "\x1b[0m"
	for _, terminalWidth := range []int{64, 80, 120, 240} {
		t.Run(fmt.Sprint(terminalWidth), func(t *testing.T) {
			for _, modal := range []string{renderModal(styled, terminalWidth), renderFlushModal(styled, terminalWidth)} {
				if width := lipgloss.Width(modal); width > min(120, terminalWidth-4) {
					t.Fatalf("modal width = %d at terminal width %d", width, terminalWidth)
				}
				// Ignore surface padding and wraps; every character must survive.
				compact := strings.Join(strings.Fields(ansi.Strip(modal)), "")
				if compact != text {
					t.Fatal("wrapping changed the modal content")
				}
			}
		})
	}
	if width := lipgloss.Width(renderModal(strings.Repeat("x", 400))); width > 120 {
		t.Fatalf("default modal width = %d", width)
	}
	if width := lipgloss.Width(renderModal("Short", 240)); width >= 120 {
		t.Fatalf("short modal expanded to %d columns", width)
	}
}

func TestLongServiceConfirmationFitsAndKeepsActions(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.height, model.ready = 40, true
	model.pendingStopTarget = strings.Repeat("service-with-long-name-", 9)
	for _, width := range []int{64, 80, 240} {
		model.width = width
		plain := ansi.Strip(model.renderConfirmServiceStopView())
		for _, expected := range []string{"[Enter/y] Stop", "[Esc/n] Cancel", "Running services will be stopped."} {
			if !strings.Contains(plain, expected) {
				t.Fatalf("width %d lost %q", width, expected)
			}
		}
		// Confirm that the panel itself leaves room on both sides.
		modal := model.renderConfirmationModal(model.pendingStopTarget, nil, "[Esc] Cancel")
		if got := lipgloss.Width(modal); got > min(120, width-4) {
			t.Fatalf("width %d: confirmation width = %d", width, got)
		}
	}
}

func TestThemePickerReflowsControlsInNarrowTerminal(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 64, 24, true
	model.settingsPath = "/tmp/settings.yaml"
	model.configPaths = []string{"/tmp/kranz.yaml"}
	model.openThemePicker()
	plain := ansi.Strip(model.renderThemeView())
	for _, expected := range []string{"[p] Theme: Project / Selected", "[m] Mode: Auto / Dark / Light", "[Shift+A] Edit color", "[Shift+B] Edit color", "[Enter] Apply", "[Esc] Cancel", "[g] Global", "[c] Project", "Global: /tmp/settings.yaml", "Project: /tmp/kranz.yaml"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("narrow theme picker lost %q:\n%s", expected, plain)
		}
	}
}
