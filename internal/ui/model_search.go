package ui

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/config"
)

// The regex log search: the line editor, applying and clearing the pattern,
// match navigation, and the blink that answers a click the modal editor cannot
// act on.

func (m *Model) handleSearchNavigationKey(msg tea.KeyMsg) bool {
	if m.panelFocus != panelLogs || m.searchMode != searchHighlight || m.logSearcher == nil || !m.logSearcher.HasPattern() {
		return false
	}
	svc := m.FocusedService()
	if svc == nil {
		return false
	}
	switch msg.String() {
	case "n":
		m.currentMatch = m.logSearcher.FindNext(m.serviceLogLines(svc), m.currentMatch)
		m.focusLogMatch(m.currentMatch)
		return true
	case "N":
		m.currentMatch = m.logSearcher.FindPrev(m.serviceLogLines(svc), m.currentMatch)
		m.focusLogMatch(m.currentMatch)
		return true
	default:
		return false
	}
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
	m.searchInput.SetValue(m.logSearcher.Pattern())
	m.searchInput.CursorEnd()
	command := m.searchInput.Focus()
	if m.panelFocus != panelPinnedLogs {
		m.panelFocus = panelLogs
	}
	return command
}

func (m *Model) applySearchQuery() bool {
	if err := m.logSearcher.SetPattern(m.searchInput.Value()); err != nil {
		m.addNotification("search", err.Error(), config.LogError)
		return false
	}
	m.currentMatch = -1
	m.logOffset = 0
	m.logAnchor = 0
	m.followMode = true
	m.logPaused = false
	if m.searchMode == searchHighlight && m.logSearcher.HasPattern() {
		if svc := m.FocusedService(); svc != nil {
			m.currentMatch = m.logSearcher.FindNext(m.serviceLogLines(svc), -1)
			m.focusLogMatch(m.currentMatch)
		}
	}
	return true
}

// clearSearch drops the active pattern and restores unfiltered following.
func (m *Model) clearSearch() {
	m.currentMatch = -1
	m.searchInput.SetValue("")
	_ = m.logSearcher.SetPattern("")
	m.followMode, m.logPaused, m.logOffset, m.logAnchor = true, false, 0, 0
}

func (m *Model) handleSearchKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// These three own the editor's lifecycle and must be claimed before the
	// text input sees them; Tab in particular is a suggestion key upstream.
	switch msg.String() {
	case "esc":
		// Esc cancels the edit rather than applying it, keeping Enter the only
		// apply. Restoring the query to the active pattern means reopening the
		// editor always shows the filter that is actually in effect.
		m.searchInput.SetValue(m.logSearcher.Pattern())
		m.searchInput.Blur()
		m.searchNudge = time.Time{}
		m.mode = ModeNormal
		m.panelFocus = panelLogs
		return m, nil
	case "tab", "shift+tab":
		if m.searchMode == searchFilter {
			m.searchMode = searchHighlight
		} else {
			m.searchMode = searchFilter
		}
		m.syncSearchInputWidth()
		// Switching to highlight over an already applied pattern should land on
		// a match instead of waiting for the next apply.
		if m.searchMode == searchHighlight && m.logSearcher.HasPattern() {
			if svc := m.FocusedService(); svc != nil {
				m.currentMatch = m.logSearcher.FindNext(m.serviceLogLines(svc), -1)
				m.focusLogMatch(m.currentMatch)
			}
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
