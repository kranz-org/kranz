package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderRuntimeSwitcherView draws the `p` modal: every locally registered
// runtime, current first, live-refreshing while open (PRD 3.2).
func (m *Model) renderRuntimeSwitcherView() string {
	lines := []string{ModalTitleStyle.Render(" Switch runtime "), ""}
	if m.switcherErr != "" {
		lines = append(lines, ContextBarStyle.Render("  "+m.switcherErr), "")
	} else if m.switcherLoading && len(m.switcherRows) == 0 {
		lines = append(lines, "  Discovering local runtimes…", "")
	}
	rowLines := renderRuntimeRowLines(m.switcherRows, m.switcherCursor, m.runListCapacity(len(lines)), m.width)
	if len(rowLines) == 0 && m.switcherErr == "" && !m.switcherLoading {
		lines = append(lines, "  No other local runtimes are registered")
	} else {
		lines = append(lines, rowLines...)
	}
	connecting := ""
	if m.switcherConnecting != "" {
		connecting = "  Connecting…"
	}
	lines = append(lines, "", runtimeModalShortcuts("  [↑/↓ · j/k] Select  [Enter] Connect  [Esc] Cancel"+connecting))
	return m.placeOverlay(renderFlushModal(strings.Join(lines, "\n")))
}

// runtimeModalShortcuts renders a runtime modal's footer with the same dim
// text and accent-coloured keys the run history modal uses, so the two read
// as the same control strip rather than two conventions.
func runtimeModalShortcuts(value string) string {
	return renderModalShortcuts(value, lipgloss.NewStyle().Foreground(ColorDim))
}

// renderRuntimeRowLines renders a windowed, cursor-highlighted list of rows
// shared by the switcher, the recovery screen's "choose running runtime",
// and the bare-launch runtime picker (none of which share a common *Model).
func renderRuntimeRowLines(rows []runtimeRow, cursor int, capacity int, width int) []string {
	if len(rows) == 0 {
		return nil
	}
	rowWidth := max(20, width-4)
	start, visible, windowed := runListWindow(len(rows), cursor, max(1, capacity-1))
	lines := make([]string, 0, visible+2)
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
	return lines
}

// renderRuntimeLostView draws the recovery screen shown once the current
// runtime's connection ends (PRD 3.5): "Restart runtime", "Choose running
// runtime", or the embedded list once the latter is chosen.
func (m *Model) renderRuntimeLostView() string {
	title := m.recoveryReason
	if title == "" {
		title = "Runtime stopped"
	}
	lines := []string{ModalTitleStyle.Render(" " + title + " "), ""}
	if m.recoveryShowingList {
		lines = append(lines, "  Choose a running runtime:", "")
		rowLines := renderRuntimeRowLines(m.switcherRows, m.switcherCursor, m.runListCapacity(len(lines)), m.width)
		if len(rowLines) == 0 {
			lines = append(lines, "  No other local runtimes are registered")
		} else {
			lines = append(lines, rowLines...)
		}
		lines = append(lines, "", runtimeModalShortcuts("  [↑/↓ · j/k] Select  [Enter] Connect  [Esc] Back"))
		return m.placeOverlay(renderFlushModal(strings.Join(lines, "\n")))
	}
	lines = append(lines, "  The connection to this project's runtime ended.", "")
	switch {
	case m.recoveryBusy:
		lines = append(lines, "  Restarting…", "")
	case m.recoveryErr != "":
		lines = append(lines, ContextBarStyle.Render("  "+m.recoveryErr), "")
	}
	lines = append(lines, runtimeModalShortcuts("  [r/Enter] Restart runtime  [c] Choose running runtime  [q] Quit TUI"))
	return m.placeOverlay(renderFlushModal(strings.Join(lines, "\n")))
}
