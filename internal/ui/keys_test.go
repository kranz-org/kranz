package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNormalizeRussianShortcutKeys(t *testing.T) {
	if got, want := len([]rune(russianShortcutRunes)), len([]rune(latinShortcutRunes)); got != want {
		t.Fatalf("layout table has %d source runes and %d targets", got, want)
	}
	for _, testCase := range []struct {
		input rune
		want  string
	}{
		{input: 'й', want: "q"},
		{input: 'Й', want: "Q"},
		{input: 'ы', want: "s"},
		{input: 'Ы', want: "S"},
		{input: 'ц', want: "w"},
		{input: '.', want: "/"},
		{input: ',', want: "?"},
	} {
		msg := normalizeShortcutKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{testCase.input}})
		if got := msg.String(); got != testCase.want {
			t.Errorf("normalize %q = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}

func TestRussianLayoutWorksOutsideTextEntry(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.mode = ModeHealthHistory
	pressKey(model, 'й') // physical q
	if model.mode != ModeNormal {
		t.Fatalf("Russian q did not close the overlay: mode %v", model.mode)
	}

	model.mode = ModeSearch
	pressKey(model, 'й')
	if model.searchQuery != "й" {
		t.Fatalf("search text was layout-normalized to %q", model.searchQuery)
	}
}
