package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestLogTextSelectionContrastsWithEveryThemeAndCanvas(t *testing.T) {
	for _, name := range ThemeNames() {
		for _, canvas := range []string{"", "#FAF9F5", "#FFFFFF", "#111111", "#777777", "#FF8800"} {
			for _, accent := range []string{"", "#FEFEFE", "#111111", "#7AA2F7", "#FFFF00"} {
				for _, dark := range []bool{false, true} {
					theme, err := BuildTheme(name, accent, canvas, dark)
					if err != nil {
						t.Fatal(err)
					}
					background, text := logTextSelectionColors(theme)
					if ratio := contrastRatio(background, theme.Surface); ratio < 3 {
						t.Fatalf("%s canvas %s accent %s: fill contrast %.2f", name, theme.Surface, theme.Accent, ratio)
					}
					if ratio := contrastRatio(text, background); ratio < 4.5 {
						t.Fatalf("%s canvas %s accent %s: text contrast %.2f", name, theme.Surface, theme.Accent, ratio)
					}
					if contrastRatio(theme.Accent, theme.Surface) >= 3 && background != theme.Accent {
						t.Fatal("an already visible accent was unnecessarily changed")
					}
				}
			}
		}
	}
}

func TestLogTextSelectionRendersThemeColorsOnNearWhiteAndDarkCanvas(t *testing.T) {
	restoreDefaultTheme(t)
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	m := selectionTestModel(t, "prefix selected suffix")
	m.beginLogSelection()
	s := m.logSelection
	s.anchor = logPoint{0, 7}
	s.cursor = logPoint{0, 15}
	for _, canvas := range []string{"#FAF9F5", "#151A21", "#777777"} {
		theme, err := ApplyThemeVariant(DefaultTheme, "#7AA2F7", canvas, true, false)
		if err != nil {
			t.Fatal(err)
		}
		background, text := logTextSelectionColors(theme)
		frame := m.View()
		if LogTextSelectionStyle.GetBackground() != lipgloss.Color(background) || LogTextSelectionStyle.GetForeground() != lipgloss.Color(text) {
			t.Fatalf("%s: selection style lost the derived palette", canvas)
		}
		// The terminal color converter can round an RGB channel by one. Compare
		// the actual emitted style, not a hand-written RGB escape sequence.
		if !strings.Contains(frame, LogTextSelectionStyle.Render("s")) {
			t.Fatalf("%s: rendered selection lost its theme style", canvas)
		}

		if s.text() != "selected" {
			t.Fatal("appearance changed selected text")
		}
	}
}
