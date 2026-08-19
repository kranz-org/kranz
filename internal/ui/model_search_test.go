package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// Tests for the regex log search.

func TestFooterPrioritizesRegexHint(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	footer := ansi.Strip(model.contextMessage())
	if !strings.Contains(footer, "[/] regex filter") {
		t.Fatalf("footer does not expose regex grep: %q", footer)
	}
	for _, numberHint := range []string{"[1]", "[2]", "[3]"} {
		if strings.Contains(footer, numberHint) {
			t.Fatalf("footer still contains panel hint %q: %q", numberHint, footer)
		}
	}
}

func TestRegexSearchFiltersMatchingLogsByDefault(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	model.app.AppendLogForTest(model.FocusedService().Name, "request complete")
	model.app.AppendLogForTest(model.FocusedService().Name, "ERROR database unavailable")

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, character := range "ERROR" {
		_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}})
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	// Enter applies the query without closing the editor so it can be refined.
	if !model.logSearcher.HasPattern() || model.searchMode != searchFilter || model.currentMatch != -1 || model.panelFocus != panelLogs || model.mode != ModeSearch {
		t.Fatalf("search state = pattern %v mode %v match %d panel %v view %v", model.logSearcher.HasPattern(), model.searchMode, model.currentMatch, model.panelFocus, model.mode)
	}
	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 12))
	if !strings.Contains(plain, "ERROR database unavailable") || strings.Contains(plain, "request complete") || !strings.Contains(plain, "FILTER /ERROR/ · 1") {
		t.Fatalf("filter mode rendered unexpected logs:\n%s", plain)
	}
	if !model.followMode || strings.Contains(plain, "PAUSED") || strings.Contains(plain, "BROWSING") {
		t.Fatalf("applying a filter changed follow state:\n%s", plain)
	}
}

// newFilteredSearchModel opens the search editor over two log lines and applies
// a pattern matching exactly one of them.
func newFilteredSearchModel(t *testing.T) *Model {
	t.Helper()
	model := newTestModel()
	model.width, model.height, model.ready = 80, 24, true
	model.app.AppendLogForTest(model.FocusedService().Name, "request complete")
	model.app.AppendLogForTest(model.FocusedService().Name, "ERROR database unavailable")
	pressKey(model, '/')
	for _, character := range "ERROR" {
		pressKey(model, character)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if !model.logSearcher.HasPattern() {
		t.Fatalf("setup did not apply a pattern")
	}
	return model
}

func TestSearchEnterKeepsEditorOpenForRefinement(t *testing.T) {
	model := newFilteredSearchModel(t)
	defer model.Shutdown()

	if model.mode != ModeSearch {
		t.Fatalf("mode after apply = %v, want ModeSearch", model.mode)
	}
	// Refining the query without reopening the editor is the point of the
	// in-place apply, so a second Enter must narrow the active pattern.
	for _, character := range " database" {
		pressKey(model, character)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if got := model.logSearcher.Pattern(); got != "ERROR database" {
		t.Fatalf("refined pattern = %q, want %q", got, "ERROR database")
	}
	if model.mode != ModeSearch {
		t.Fatalf("mode after refinement = %v, want ModeSearch", model.mode)
	}
}

func TestSearchForwardsTextInputCommandsAndMessages(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true

	_, focusCommand := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if focusCommand == nil {
		t.Fatal("opening the search editor discarded the text input focus command")
	}

	// Clipboard paste results and cursor blink events are private textinput
	// messages delivered through the same parent update path. The exported
	// initial blink message verifies that path without touching the clipboard.
	_, blinkCommand := model.Update(textinput.Blink())
	if blinkCommand == nil {
		t.Fatal("the parent update loop did not forward a text input message")
	}
}

func TestSearchCursorBlinkKeepsHintsInSearchColor(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)
	restoreDefaultTheme(t)
	if _, err := ApplyTheme("nord", ""); err != nil {
		t.Fatal(err)
	}

	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 120, 24, true
	pressKey(model, '/')
	pressKey(model, 'x')

	assertHintStyle := func(phase string) {
		t.Helper()
		rows := strings.Split(model.renderSearchView(), "\n")
		bar := rows[len(rows)-1]
		hintAt := strings.Index(bar, "[Tab]")
		if hintAt < 0 {
			t.Fatalf("%s cursor phase has no search hints: %q", phase, ansi.Strip(bar))
		}
		const reset = "\x1b[0m"
		resetAt := strings.LastIndex(bar[:hintAt], reset)
		if resetAt < 0 {
			t.Fatalf("%s cursor phase has no nested style reset", phase)
		}
		prefix := terminalStylePrefix(SearchInputStyle)
		if !strings.HasPrefix(bar[resetAt+len(reset):], prefix) {
			t.Fatalf("%s cursor phase does not restore the search color before hints", phase)
		}
	}

	assertHintStyle("visible")
	_, _ = model.Update(textinput.Blink())
	assertHintStyle("hidden")
}

func TestSearchEscapeLeavesEditorKeepingActiveFilter(t *testing.T) {
	model := newFilteredSearchModel(t)
	defer model.Shutdown()

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEsc})
	if model.mode != ModeNormal {
		t.Fatalf("mode after Esc = %v, want ModeNormal", model.mode)
	}
	if !model.logSearcher.HasPattern() || model.logSearcher.Pattern() != "ERROR" {
		t.Fatalf("Esc dropped the filter: pattern %q", model.logSearcher.Pattern())
	}
	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 12))
	if !strings.Contains(plain, "ERROR database unavailable") || strings.Contains(plain, "request complete") {
		t.Fatalf("filter is no longer applied after leaving the editor:\n%s", plain)
	}
}

func TestSearchEscapeDiscardsUnappliedEdits(t *testing.T) {
	model := newFilteredSearchModel(t)
	defer model.Shutdown()

	// Enter is the only way to apply. Esc cancels the edit and rewinds the
	// query to the active pattern, so reopening never shows a stale draft.
	for range len("ERROR") {
		_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	for _, character := range "request" {
		pressKey(model, character)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEsc})
	if got := model.logSearcher.Pattern(); got != "ERROR" {
		t.Fatalf("pattern after Esc = %q, want the unchanged %q", got, "ERROR")
	}
	if model.searchInput.Value() != "ERROR" {
		t.Fatalf("query after Esc = %q, want it rewound to the active pattern", model.searchInput.Value())
	}
	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 12))
	if !strings.Contains(plain, "ERROR database unavailable") || strings.Contains(plain, "request complete") {
		t.Fatalf("Esc applied a query it should have discarded:\n%s", plain)
	}
}

func TestSearchEscapeInNormalModeClearsFilter(t *testing.T) {
	model := newFilteredSearchModel(t)
	defer model.Shutdown()

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEsc})
	// The second Esc is the reset step, and it must restore following.
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEsc})
	if model.logSearcher.HasPattern() || model.searchInput.Value() != "" {
		t.Fatalf("filter survived the clearing Esc: pattern %q query %q", model.logSearcher.Pattern(), model.searchInput.Value())
	}
	if !model.followMode || model.logPaused {
		t.Fatalf("clearing left follow state off: follow %v paused %v", model.followMode, model.logPaused)
	}
	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 12))
	if !strings.Contains(plain, "request complete") || !strings.Contains(plain, "ERROR database unavailable") {
		t.Fatalf("clearing did not restore unfiltered logs:\n%s", plain)
	}
}

func TestSearchCtrlUErasesQueryWithoutLeavingEditor(t *testing.T) {
	model := newFilteredSearchModel(t)
	defer model.Shutdown()

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlU})
	if model.searchInput.Value() != "" {
		t.Fatalf("query after Ctrl+U = %q, want empty", model.searchInput.Value())
	}
	if model.mode != ModeSearch {
		t.Fatalf("Ctrl+U left the editor: mode %v", model.mode)
	}
	// Erasing alone must not change what is filtered until it is applied.
	if !model.logSearcher.HasPattern() {
		t.Fatalf("Ctrl+U dropped the active pattern before apply")
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if model.logSearcher.HasPattern() {
		t.Fatalf("applying an empty query left a pattern active")
	}
}

func TestSearchAcceptsAlternationAndPastedText(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown() //nolint:errcheck // test cleanup; a shutdown failure surfaces through the assertions themselves.
	model.width, model.height, model.ready = 100, 24, true

	pressKey(model, '/')
	for _, character := range "GET|PATCH" {
		pressKey(model, character)
	}
	// A paste arrives as one message carrying every rune at once.
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("|HEAD"), Paste: true})
	if got := model.searchInput.Value(); got != "GET|PATCH|HEAD" {
		t.Fatalf("query = %q, want the typed alternation plus the pasted branch", got)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if got := model.logSearcher.Pattern(); got != "GET|PATCH|HEAD" {
		t.Fatalf("applied pattern = %q", got)
	}
}

func TestSearchCursorCanEditMidPattern(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown() //nolint:errcheck // test cleanup; a shutdown failure surfaces through the assertions themselves.
	model.width, model.height, model.ready = 100, 24, true

	pressKey(model, '/')
	for _, character := range "GET|PATCH" {
		pressKey(model, character)
	}
	// Anchoring an alternation means editing both ends, not just retyping it.
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyHome})
	for _, character := range "^(" {
		pressKey(model, character)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnd})
	pressKey(model, ')')
	if got := model.searchInput.Value(); got != "^(GET|PATCH)" {
		t.Fatalf("query = %q, want the pattern edited at both ends", got)
	}
}

func TestSearchKeepsCaretVisibleOnLongPattern(t *testing.T) {
	const long = "GET|POST|PATCH|DELETE|OPTIONS|HEAD|TRACE|CONNECT|PROPFIND|MKCOL|COPY|MOVE|LOCK"
	// 64 columns is the narrowest dashboard Kranz renders at all.
	for _, width := range []int{64, 80, 120} {
		model := newTestModel()
		model.width, model.height, model.ready = width, 24, true
		pressKey(model, '/')
		for _, character := range long {
			pressKey(model, character)
		}
		bar := ""
		for _, line := range strings.Split(ansi.Strip(model.View()), "\n") {
			if strings.Contains(line, "Regex") {
				bar = line
			}
		}
		// The editor scrolls under the caret, so the tail being typed must stay
		// on screen no matter how narrow the terminal is.
		if !strings.Contains(bar, "LOCK") {
			t.Errorf("width %d hid the end of the pattern being typed:\n%s", width, strings.TrimRight(bar, " "))
		}
		if lipgloss.Width(bar) > width {
			t.Errorf("width %d overflowed the search bar to %d columns", width, lipgloss.Width(bar))
		}
		_ = model.Shutdown()
	}
}

func TestSearchClickOutsideEditorNudgesInsteadOfDoingNothing(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown() //nolint:errcheck // test cleanup; a shutdown failure surfaces through the assertions themselves.
	model.width, model.height, model.ready = 100, 24, true
	pressKey(model, '/')
	if model.searchNudgeActive() {
		t.Fatal("opening the editor should not flash the panel")
	}

	press := tea.MouseMsg{X: 20, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	_, command := model.handleMouseMsg(press)
	if model.mode != ModeSearch {
		t.Fatalf("a click outside the editor left search mode: %v", model.mode)
	}
	if !model.searchNudgeActive() {
		t.Fatal("a click the editor could not act on gave no feedback")
	}
	if command == nil {
		t.Fatal("no repaint was scheduled, so the blink could never animate")
	}

	// It has to blink rather than simply light up, so count the rising edges
	// across the window and hold them to the configured pulse count.
	start := model.searchNudge
	pulses, previous := 0, false
	for offset := time.Duration(0); offset < searchNudgeDuration; offset += searchNudgeBlink / 3 {
		model.searchNudge = start.Add(-offset)
		lit := model.searchNudgeActive()
		if lit && !previous {
			pulses++
		}
		previous = lit
	}
	if pulses != searchNudgePulses {
		t.Fatalf("border pulsed %d times, want %d", pulses, searchNudgePulses)
	}

	// The blink drives its own tick, so it must stop once the window closes.
	model.searchNudge = start
	expired := start.Add(-searchNudgeDuration - time.Millisecond)
	model.searchNudge = expired
	if model.searchNudgeActive() {
		t.Fatal("the blink outlasted its duration")
	}
	if _, command := model.Update(searchNudgeMsg(expired)); command != nil {
		t.Fatal("the blink kept rescheduling after it ended")
	}
	if !model.searchNudge.IsZero() {
		t.Fatal("the finished blink was not cleared")
	}

	// Clicking a real control acts on it instead of flashing.
	_, _ = model.handleMouseMsg(press)
	_ = clickRenderedText(t, model, "[Esc] done")
	if model.mode != ModeNormal {
		t.Fatalf("clicking the exit control did not close the editor: %v", model.mode)
	}
	if model.searchNudgeActive() {
		t.Fatal("leaving the editor left the flash behind")
	}
}

func TestSearchInvalidQueryKeepsLastAppliedPattern(t *testing.T) {
	model := newFilteredSearchModel(t)
	defer model.Shutdown()

	pressKey(model, '[')
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if got := model.logSearcher.Pattern(); got != "ERROR" {
		t.Fatalf("invalid query changed the active pattern to %q", got)
	}
	if model.mode != ModeSearch {
		t.Fatalf("invalid query closed the editor: mode %v", model.mode)
	}
	if len(model.notifications) == 0 {
		t.Fatalf("invalid query produced no notification")
	}
}

func TestRegexTabEnablesHighlightModeWithoutFalsePausedLabel(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 12, true
	for index := range 20 {
		line := fmt.Sprintf("line %02d", index)
		if index == 3 {
			line = "ERROR database unavailable"
		}
		model.app.AppendLogForTest(model.FocusedService().Name, line)
	}

	pressKey(model, '/')
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyTab})
	for _, character := range "ERROR" {
		pressKey(model, character)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})

	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 10))
	if model.searchMode != searchHighlight || model.currentMatch != 3 || !strings.Contains(plain, "HIGHLIGHT /ERROR/ · 1") {
		t.Fatalf("highlight state = mode %v match %d:\n%s", model.searchMode, model.currentMatch, plain)
	}
	if strings.Contains(plain, "PAUSED") || !strings.Contains(plain, "BROWSING") {
		t.Fatalf("match navigation should be labeled BROWSING, not PAUSED:\n%s", plain)
	}
}
