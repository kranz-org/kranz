package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kranz-org/kranz/internal/config"
)

func TestParseTerminalBackground(t *testing.T) {
	tests := []struct {
		name     string
		response string
		dark     bool
	}{
		{name: "dark 16 bit", response: "\x1b]11;rgb:0808/1010/1818\x1b\\", dark: true},
		{name: "light 16 bit", response: "\x1b]11;rgb:f5f5/f2f2/e8e8\a", dark: false},
		{name: "dark 8 bit", response: "\x1b]11;rgb:12/24/36\x1b\\", dark: true},
		{name: "light 4 bit", response: "noise\x1b]11;rgb:f/e/d\a", dark: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dark, err := parseTerminalBackground(test.response)
			if err != nil {
				t.Fatal(err)
			}
			if dark != test.dark {
				t.Fatalf("dark = %v, want %v", dark, test.dark)
			}
		})
	}
	for _, response := range []string{"", "\x1b]11;#ffffff\a", "\x1b]11;rgb:ff/ff\a"} {
		if _, err := parseTerminalBackground(response); err == nil {
			t.Errorf("parseTerminalBackground(%q) accepted invalid response", response)
		}
	}
}

func TestBackgroundColorMessageReappliesAutomaticPalette(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	light := false
	model := NewModelWithOptions(&config.Config{
		Project: "Adaptive", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
		Services: map[string]config.Service{"api": {Command: "true"}},
	}, "test", ModelOptions{DarkBackground: &light})
	defer model.Shutdown()
	lightBackground := model.activeTheme.Background

	_, command := model.Update(backgroundColorMsg{dark: true})
	if command == nil || !model.terminalDark {
		t.Fatal("dark terminal update did not repaint the model")
	}
	if model.activeTheme.Background == lightBackground {
		t.Fatalf("background remained %s after dark terminal update", lightBackground)
	}
	if !strings.Contains(model.toastMessage, "dark") {
		t.Fatalf("appearance notification = %q", model.toastMessage)
	}
}

func TestSystemAppearanceChangeReappliesPaletteWithoutFocus(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	light := false
	model := NewModelWithOptions(&config.Config{
		Project: "Adaptive", UI: config.UIConfig{Theme: "forest"},
		Services: map[string]config.Service{"api": {Command: "true"}},
	}, "test", ModelOptions{DarkBackground: &light})
	defer model.Shutdown()
	lightBackground := model.activeTheme.Background

	_, _ = model.Update(systemAppearanceMsg{dark: false, available: true})
	if !model.systemAppearanceSet {
		t.Fatal("initial system appearance was not recorded")
	}
	_, command := model.Update(systemAppearanceMsg{dark: true, available: true})
	if command == nil || !model.terminalDark {
		t.Fatal("system appearance change did not repaint the model")
	}
	if model.activeTheme.Background == lightBackground {
		t.Fatalf("background remained %s after automatic dark appearance", lightBackground)
	}
	if !strings.Contains(model.toastMessage, "System appearance changed to dark") {
		t.Fatalf("appearance notification = %q", model.toastMessage)
	}
}

func TestAutomaticPaintedThemeFollowsDetectedAppearance(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	dark := true
	model := NewModelWithOptions(&config.Config{
		Project: "Painted", UI: config.UIConfig{Theme: "cream", Background: "theme", ColorMode: "auto"},
		Services: map[string]config.Service{"api": {Command: "true"}},
	}, "test", ModelOptions{DarkBackground: &dark})
	defer model.Shutdown()
	if model.activeTheme.TerminalCanvas {
		t.Fatal("painted theme unexpectedly inherited the terminal canvas")
	}
	darkBackground := model.activeTheme.Background

	_, command := model.Update(backgroundColorMsg{dark: false})
	if command == nil || model.activeTheme.Background == darkBackground {
		t.Fatalf("automatic painted theme did not switch to light: %#v", model.activeTheme)
	}
}

func TestForcedColorModeIgnoresDetectedAppearanceChanges(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	dark := true
	model := NewModelWithOptions(&config.Config{
		Project: "Forced", UI: config.UIConfig{Theme: "cream", Background: "theme", ColorMode: "dark"},
		Services: map[string]config.Service{"api": {Command: "true"}},
	}, "test", ModelOptions{DarkBackground: &dark})
	defer model.Shutdown()
	background := model.activeTheme.Background

	_, command := model.Update(backgroundColorMsg{dark: false})
	if command != nil || model.activeTheme.Background != background {
		t.Fatalf("forced dark mode reacted to detection: command=%v theme=%#v", command, model.activeTheme)
	}
}

func TestFocusSchedulesFreshTerminalBackgroundProbe(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	model := newTestModel()
	defer model.Shutdown()
	model.lastBackgroundProbe = time.Now().Add(-2 * time.Second)

	_, command := model.Update(tea.FocusMsg{})
	if command == nil || !model.backgroundProbeBusy {
		t.Fatal("focus did not schedule a terminal background probe")
	}
}
