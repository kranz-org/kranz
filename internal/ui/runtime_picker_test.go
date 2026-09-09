package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestRuntimePickerEscapeQuitsAndIsAdvertised(t *testing.T) {
	picker := &RuntimePicker{width: 120, height: 24, ready: true}

	_, cmd := picker.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("Escape did not return a quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("Escape command returned %T, want tea.QuitMsg", msg)
	}

	plain := ansi.Strip(picker.View())
	if !strings.Contains(plain, "press Esc or q to quit") || !strings.Contains(plain, "[Esc/q] Quit") {
		t.Fatalf("runtime picker does not advertise Escape: %q", plain)
	}
}

func TestRuntimePickerRestoresCanvasBackgroundAfterModalReset(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)
	restoreDefaultTheme(t)

	for _, themeName := range []string{"nord", "github-light"} {
		t.Run(themeName, func(t *testing.T) {
			if _, err := ApplyTheme(themeName, ""); err != nil {
				t.Fatal(err)
			}

			picker := &RuntimePicker{width: 140, height: 24, ready: true}
			rendered := picker.View()
			canvasPrefix := terminalStylePrefix(lipgloss.NewStyle().Background(ColorBackground))
			if canvasPrefix == "" {
				t.Fatal("painted theme has no canvas background prefix")
			}

			var instructionLine string
			for _, line := range strings.Split(rendered, "\n") {
				if strings.Contains(line, "No Kranz configuration") {
					instructionLine = line
					break
				}
			}
			if instructionLine == "" {
				t.Fatal("runtime picker instruction line was not rendered")
			}
			if !strings.Contains(instructionLine, "\x1b[0m"+canvasPrefix) {
				t.Fatalf("canvas background is not restored after the modal reset: %q", instructionLine)
			}
		})
	}
}
