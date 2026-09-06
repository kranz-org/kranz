package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// renderRuntimeSwitcherView draws the `p` modal: every locally registered
// runtime, current first, live-refreshing while open (PRD 3.2).
func (m *Model) renderRuntimeSwitcherView() string {
	contentWidth := flushModalContentWidth(m.width, 110)
	shortcutGroups := []string{"[↑/↓ · j/k] Select", "[Enter] Connect", "[Esc] Cancel"}
	if m.switcherConnecting != "" {
		shortcutGroups = append(shortcutGroups, "Connecting…")
	}
	shortcutRows := renderModalShortcutRows(shortcutGroups, contentWidth, lipgloss.NewStyle().Foreground(ColorDim))
	lines := []string{ansi.Truncate(ModalTitleStyle.Render(" Switch runtime "), contentWidth, "…"), ""}
	if m.switcherErr != "" {
		for _, line := range modalTextRows(m.switcherErr, contentWidth) {
			lines = append(lines, ContextBarStyle.Render(line))
		}
		lines = append(lines, "")
	} else if m.switcherLoading && len(m.switcherRows) == 0 {
		lines = append(lines, modalTextRows("Discovering local runtimes…", contentWidth)...)
		lines = append(lines, "")
	}
	rowLines := renderRuntimeRowLines(m.switcherRows, m.switcherCursor,
		m.runListCapacity(len(lines)+max(0, len(shortcutRows)-1)), contentWidth)
	if len(rowLines) == 0 && m.switcherErr == "" && !m.switcherLoading {
		lines = append(lines, modalTextRows("No other local runtimes are registered", contentWidth)...)
	} else {
		lines = append(lines, rowLines...)
	}
	lines = append(lines, "")
	lines = append(lines, shortcutRows...)
	return m.placeOverlay(renderFlushModal(strings.Join(lines, "\n")))
}

// renderRuntimeRowLines renders a windowed, cursor-highlighted list of rows
// shared by the switcher, the recovery screen's "choose running runtime",
// and the bare-launch runtime picker (none of which share a common *Model).
// The status column only has room for the state word, so when the cursor is
// on a row that cannot be selected, the reason behind that word is spelled
// out on its own line below the list (PRD 3.2: an unavailable runtime stays
// visible and explains why it is unavailable).
func renderRuntimeRowLines(rows []runtimeRow, cursor int, capacity int, width int) []string {
	if len(rows) == 0 {
		return nil
	}
	rowWidth := max(8, width-2)
	reason := ""
	if cursor >= 0 && cursor < len(rows) {
		reason = rows[cursor].Reason
	}
	listCapacity := max(1, capacity-1) // the header row
	if reason != "" {
		listCapacity = max(1, listCapacity-1)
	}
	start, visible, windowed := runListWindow(len(rows), cursor, listCapacity)
	lines := make([]string, 0, visible+3)
	lines = append(lines, "  "+HelpSectionStyle.Render(runtimeRowHeader(rowWidth)))
	for index := start; index < start+visible; index++ {
		row := rows[index]
		text := "  " + runtimeRowLine(row, rowWidth)
		if !row.Selectable {
			text = ContextBarStyle.Render(text)
		}
		if index == cursor {
			text = SelectionStyle.Render(text)
		}
		lines = append(lines, text)
	}
	if windowed {
		lines = append(lines, "  "+ContextBarStyle.Render(fmt.Sprintf("%d/%d", cursor+1, len(rows))))
	}
	if reason != "" {
		lines = append(lines, "  "+ContextBarStyle.Render(ansi.Truncate(reason, rowWidth, "…")))
	}
	return lines
}

// renderRuntimeLostView draws the recovery screen shown once the current
// runtime's connection ends (PRD 3.5): "Restart runtime", "Choose running
// runtime", or the embedded list once the latter is chosen.
func (m *Model) renderRuntimeLostView() string {
	contentWidth := flushModalContentWidth(m.width, 110)
	title := m.recoveryReason
	if title == "" {
		title = "Runtime stopped"
	}
	lines := []string{ansi.Truncate(ModalTitleStyle.Render(" "+title+" "), contentWidth, "…"), ""}
	if m.recoveryShowingList {
		shortcutRows := renderModalShortcutRows([]string{"[↑/↓ · j/k] Select", "[Enter] Connect", "[Esc] Back"}, contentWidth, lipgloss.NewStyle().Foreground(ColorDim))
		lines = append(lines, modalTextRows("Choose a running runtime:", contentWidth)...)
		lines = append(lines, "")
		rowLines := renderRuntimeRowLines(m.switcherRows, m.switcherCursor,
			m.runListCapacity(len(lines)+max(0, len(shortcutRows)-1)), contentWidth)
		if len(rowLines) == 0 {
			lines = append(lines, modalTextRows("No other local runtimes are registered", contentWidth)...)
		} else {
			lines = append(lines, rowLines...)
		}
		lines = append(lines, "")
		lines = append(lines, shortcutRows...)
		return m.placeOverlay(renderFlushModal(strings.Join(lines, "\n")))
	}
	lines = append(lines, modalTextRows("The connection to this project's runtime ended.", contentWidth)...)
	lines = append(lines, "")
	switch {
	case m.recoveryBusy:
		lines = append(lines, "  Restarting…", "")
	case m.recoveryErr != "":
		for _, line := range modalTextRows(m.recoveryErr, contentWidth) {
			lines = append(lines, ContextBarStyle.Render(line))
		}
		lines = append(lines, "")
	}
	lines = append(lines, renderModalShortcutRows([]string{"[r/Enter] Restart runtime", "[c] Choose running runtime", "[q] Quit TUI"}, contentWidth, lipgloss.NewStyle().Foreground(ColorDim))...)
	return m.placeOverlay(renderFlushModal(strings.Join(lines, "\n")))
}
