package ui

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	kranzlog "github.com/kranz-org/kranz/internal/log"
)

// The regex log search: the line editor, applying and clearing the pattern,
// match navigation, and the blink that answers a click the modal editor cannot
// act on.

func (m *Model) handleSearchNavigationKey(msg tea.KeyMsg) bool {
	searcher := m.activeLogSearcher()
	if (m.panelFocus != panelLogs && m.panelFocus != panelPinnedLogs) || m.activeSearchMode() != searchHighlight || searcher == nil || !searcher.HasPattern() {
		return false
	}
	lines := m.activeSearchLines()
	if len(lines) == 0 {
		return false
	}
	match := m.activeMatchPointer()
	switch msg.String() {
	case "n":
		*match = searcher.FindNext(lines, *match)
		m.focusActiveLogMatch(*match)
		return true
	case "N":
		*match = searcher.FindPrev(lines, *match)
		m.focusActiveLogMatch(*match)
		return true
	default:
		return false
	}
}

func (m *Model) activeLogSearcher() *kranzlog.Searcher {
	if m.panelFocus == panelPinnedLogs {
		return m.pinnedSearcher
	}
	return m.logSearcher
}

func (m *Model) activeSearchMode() logSearchMode {
	if m.panelFocus == panelPinnedLogs {
		return m.pinnedSearchMode
	}
	return m.searchMode
}

func (m *Model) activeSearchModePointer() *logSearchMode {
	if m.panelFocus == panelPinnedLogs {
		return &m.pinnedSearchMode
	}
	return &m.searchMode
}

func (m *Model) activeMatchPointer() *int {
	if m.panelFocus == panelPinnedLogs {
		return &m.pinnedMatch
	}
	return &m.currentMatch
}

func (m *Model) activeSearchLines() []string {
	if m.panelFocus == panelPinnedLogs {
		if target, ok := m.pinnedRunTarget(); ok {
			return logEntryLines(m.pinnedEntries(target))
		}
		return nil
	}
	if m.focusedAction != nil {
		run := uint32(0)
		if m.runMode == runViewSingle {
			run = m.selectedRun
		}
		return m.cachedActionOutputLines(app.ActionRunTarget(*m.focusedAction), run)
	}
	return m.serviceLogLines(m.FocusedService())
}

// nudgeSearchFocus answers a click that landed outside the editor while the
// search was open. The editor is modal because leaving it has to mean either
// apply or discard, and a click says neither, so the panel blinks instead of
// swallowing the click in silence.
func (m *Model) nudgeSearchFocus() tea.Cmd {
	m.searchNudge = time.Now()
	return m.scheduleSearchNudge(m.searchNudge)
}

// scheduleSearchNudge repaints on the blink interval. The dashboard's own tick
// is both too slow and unsynchronized with the click, so the blink needs its
// own beat for its phases to be visible at all.
func (m *Model) scheduleSearchNudge(start time.Time) tea.Cmd {
	return tea.Tick(searchNudgeBlink, func(time.Time) tea.Msg { return searchNudgeMsg(start) })
}

// searchNudgeActive reports whether the border is lit in the current phase.
func (m *Model) searchNudgeActive() bool {
	if m.searchNudge.IsZero() {
		return false
	}
	elapsed := time.Since(m.searchNudge)
	if elapsed >= searchNudgeDuration {
		return false
	}
	return (elapsed/searchNudgeBlink)%2 == 0
}

// applySearchQuery compiles the edited query and makes it the active pattern.
// Enter is the only way to apply, and the editor stays open afterwards so a
// pattern can be refined without reopening it. It reports whether the query
// compiled.
// newSearchInput builds the regex editor. Editing, cursor motion, and the
// horizontal window that keeps the caret visible on a long pattern all come
// from the component; Kranz only owns apply, cancel, and the mode toggle.
func newSearchInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	// A regex can legitimately be long, and the editor scrolls horizontally
	// rather than truncating, so no character limit applies.
	input.CharLimit = 0
	return input
}

// syncSearchInputWidth refreshes the editor's visible window. The component
// recomputes its horizontal scroll while handling a key, not while rendering,
// so the width has to be current before input reaches it.
func (m *Model) syncSearchInputWidth() {
	_, _, editorWidth := m.searchBarLayout()
	m.searchInput.Width = editorWidth
}

// openSearchEditor shows the editor seeded with the active pattern and focuses
// the logs it filters, so match navigation works as soon as the editor closes.
func (m *Model) openSearchEditor() tea.Cmd {
	m.mode = ModeSearch
	m.searchNudge = time.Time{}
	m.syncSearchInputWidth()
	m.searchInput.SetValue(m.activeLogSearcher().Pattern())
	m.searchInput.CursorEnd()
	command := m.searchInput.Focus()
	if m.panelFocus != panelPinnedLogs {
		m.panelFocus = panelLogs
	}
	return command
}

func (m *Model) applySearchQuery() bool {
	searcher := m.activeLogSearcher()
	if err := searcher.SetPattern(m.searchInput.Value()); err != nil {
		m.addNotification("search", err.Error(), config.LogError)
		return false
	}
	match := m.activeMatchPointer()
	*match = -1
	if m.panelFocus == panelPinnedLogs {
		m.pinnedOffset, m.pinnedAnchor, m.pinnedFollow = 0, 0, true
	} else {
		m.logOffset, m.logAnchor, m.followMode, m.logPaused = 0, 0, true, false
	}
	if m.activeSearchMode() == searchHighlight && searcher.HasPattern() {
		*match = searcher.FindNext(m.activeSearchLines(), -1)
		m.focusActiveLogMatch(*match)
	}
	return true
}

// clearSearch drops the active pattern and restores unfiltered following.
func (m *Model) clearSearch() {
	*m.activeMatchPointer() = -1
	m.searchInput.SetValue("")
	_ = m.activeLogSearcher().SetPattern("")
	if m.panelFocus == panelPinnedLogs {
		m.pinnedFollow, m.pinnedOffset, m.pinnedAnchor = true, 0, 0
	} else {
		m.followMode, m.logPaused, m.logOffset, m.logAnchor = true, false, 0, 0
	}
}

func (m *Model) handleSearchKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// These three own the editor's lifecycle and must be claimed before the
	// text input sees them; Tab in particular is a suggestion key upstream.
	switch msg.String() {
	case "esc":
		// Esc cancels the edit rather than applying it, keeping Enter the only
		// apply. Restoring the query to the active pattern means reopening the
		// editor always shows the filter that is actually in effect.
		m.searchInput.SetValue(m.activeLogSearcher().Pattern())
		m.searchInput.Blur()
		m.searchNudge = time.Time{}
		m.mode = ModeNormal
		return m, nil
	case "tab", "shift+tab":
		mode := m.activeSearchModePointer()
		if *mode == searchFilter {
			*mode = searchHighlight
		} else {
			*mode = searchFilter
		}
		m.syncSearchInputWidth()
		// Switching to highlight over an already applied pattern should land on
		// a match instead of waiting for the next apply.
		searcher := m.activeLogSearcher()
		if *mode == searchHighlight && searcher.HasPattern() {
			match := m.activeMatchPointer()
			*match = searcher.FindNext(m.activeSearchLines(), -1)
			m.focusActiveLogMatch(*match)
		}
		return m, nil
	case "enter":
		m.applySearchQuery()
		return m, nil
	}

	var command tea.Cmd
	m.searchInput, command = m.searchInput.Update(msg)
	return m, command
}
