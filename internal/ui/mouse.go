package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	dashboardHeaderRows = 1
	dashboardFooterRows = 1
	listFirstItemRow    = 3
	checkboxMinColumn   = 3
	checkboxMaxColumn   = 5
)

type mouseKeyBinding struct {
	label string
	key   string
}

// renderedTextHit maps a terminal cell back to a styled control label. Widths
// are measured after stripping ANSI because byte offsets and terminal cells
// differ for styled and wide Unicode text.
func renderedTextHit(rendered string, x, y int, label string) bool {
	lines := strings.Split(ansi.Strip(rendered), "\n")
	if y < 0 || y >= len(lines) || label == "" {
		return false
	}
	line := lines[y]
	for searchFrom := 0; searchFrom <= len(line); {
		relative := strings.Index(line[searchFrom:], label)
		if relative < 0 {
			return false
		}
		startByte := searchFrom + relative
		startCell := lipgloss.Width(line[:startByte])
		endCell := startCell + lipgloss.Width(label)
		if x >= startCell && x < endCell {
			return true
		}
		searchFrom = startByte + len(label)
	}
	return false
}

// handleMouseMsg routes mouse input through the same state transitions used by
// keyboard input. Keeping both paths aligned prevents click-only behavior from
// drifting away from documented shortcuts.
func (m *Model) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode != ModeNormal {
		return m.handleOverlayMouse(msg)
	}
	return m.handleDashboardMouse(msg)
}

func (m *Model) handleDashboardMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if model, command, handled := m.handleDashboardWheel(msg); handled {
		return model, command
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	rendered := m.View()
	if model, command, handled := m.handleDashboardTextControl(rendered, msg); handled {
		return model, command
	}
	if msg.Y == m.height-dashboardFooterRows {
		if renderedTextHit(rendered, msg.X, msg.Y, "[/] regex") {
			m.mode, m.searchQuery = ModeSearch, m.logSearcher.Pattern()
			return m, nil
		}
		return m.triggerAction(m.actionAt(msg.X))
	}
	if msg.Y < dashboardHeaderRows || msg.Y >= m.height-dashboardFooterRows {
		return m, nil
	}

	if msg.X < m.dashboardLeftWidth() {
		return m.handleLeftColumnClick(msg)
	}
	m.panelFocus = m.logPanelFocusAt(msg.Y)
	return m, nil
}

func (m *Model) handleDashboardWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	if m.mouseInDetails(msg.X, msg.Y) {
		m.panelFocus = panelDetails
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scrollDetails(-1)
			return m, nil, true
		case tea.MouseButtonWheelDown:
			m.scrollDetails(1)
			return m, nil, true
		}
	}
	if msg.X >= m.dashboardLeftWidth() && msg.Y > 0 && msg.Y < m.height-dashboardFooterRows {
		m.panelFocus = m.logPanelFocusAt(msg.Y)
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scrollLogs(-1)
			return m, nil, true
		case tea.MouseButtonWheelDown:
			m.scrollLogs(1)
			return m, nil, true
		}
	}
	return m, nil, false
}

func (m *Model) handleDashboardTextControl(rendered string, msg tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	switch {
	case renderedTextHit(rendered, msg.X, msg.Y, "[?] help"):
		m.helpOffset = 0
		m.mode = ModeHelp
	case renderedTextHit(rendered, msg.X, msg.Y, "[1]"):
		if m.panelFocus == panelServices {
			m.toggleListMode()
		} else {
			m.panelFocus = panelServices
		}
	case renderedTextHit(rendered, msg.X, msg.Y, "[2]"):
		m.panelFocus = panelDetails
	case renderedTextHit(rendered, msg.X, msg.Y, "[3]"):
		m.panelFocus = m.logPanelFocusAt(msg.Y)
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m *Model) logPanelFocusAt(y int) panelFocus {
	if m.PinnedService() == nil {
		return panelLogs
	}
	if y < dashboardHeaderRows+(m.height-dashboardHeaderRows-dashboardFooterRows)/2 {
		return panelPinnedLogs
	}
	return panelLogs
}

func (m *Model) handleLeftColumnClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	listHeight, _ := m.serviceColumnLayout(m.height - dashboardHeaderRows - dashboardFooterRows)
	if msg.Y >= dashboardHeaderRows+listHeight {
		m.panelFocus = panelDetails
		return m, nil
	}
	m.panelFocus = panelServices
	row := msg.Y - listFirstItemRow
	if row < 0 {
		return m, nil
	}
	if m.listMode == listTags {
		m.handleTagRowClick(row, listHeight, msg.X)
	} else {
		m.handleServiceRowClick(row, listHeight, msg.X)
	}
	return m, nil
}

func (m *Model) handleServiceRowClick(row, listHeight, column int) {
	start, end := m.visibleServiceRange(listHeight)
	index := start + row
	if index < start || index >= end {
		return
	}
	m.moveFocus(index)
	if column >= checkboxMinColumn && column <= checkboxMaxColumn {
		m.toggleFocusedSelection()
	}
}

func (m *Model) handleTagRowClick(row, listHeight, column int) {
	start, end := m.visibleTagRange(listHeight)
	index := start + row
	if index < start || index >= end {
		return
	}
	m.tagCursor = index
	if column >= checkboxMinColumn && column <= checkboxMaxColumn {
		m.toggleCurrentSelection()
	}
}

func (m *Model) handleOverlayMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if model, command, handled := m.handleOverlayWheel(msg); handled {
		return model, command
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	rendered := m.View()
	switch m.mode {
	case ModeThemes:
		return m.handleThemeMouseClick(rendered, msg)
	case ModeSearch:
		return m.handleMouseKeyBindings(rendered, msg, []mouseKeyBinding{
			{label: "[Tab]", key: "tab"},
			{label: "[Enter] apply", key: "enter"},
			{label: "[Esc] clear", key: "esc"},
		}, m.handleSearchKeys)
	case ModeHelp:
		if model, command, handled := m.handleMouseKeyBindingsHandled(rendered, msg, []mouseKeyBinding{
			{label: "[↑/k] Up", key: "up"},
			{label: "[↓/j] Down", key: "down"},
		}, m.handleHelpKeys); handled {
			return model, command
		}
		return m.closeOverlayOnClick(rendered, msg)
	case ModeHealthHistory, ModeNotifications:
		return m.closeOverlayOnClick(rendered, msg)
	case ModeConfirmQuit:
		if renderedTextHit(rendered, msg.X, msg.Y, "[Enter/y] Stop everything and quit") {
			return m.beginShutdown()
		}
		if renderedTextHit(rendered, msg.X, msg.Y, "[Esc/n]   Stay here") {
			m.mode = ModeNormal
		}
	case ModeConfirmRestart:
		if renderedTextHit(rendered, msg.X, msg.Y, "[Enter/y] Continue") {
			return m.beginRestart(m.confirmTarget)
		}
		if renderedTextHit(rendered, msg.X, msg.Y, "[Esc/n] Cancel") {
			m.mode = ModeNormal
		}
	case ModePortConflict:
		return m.handleMouseKeyBindings(rendered, msg, []mouseKeyBinding{
			{label: "[k] Stop this external process and retry", key: "k"},
			{label: "[r/Enter] Retry", key: "r"},
			{label: "[s/Esc] Close", key: "s"},
		}, m.handlePortConflictKeys)
	}
	return m, nil
}

func (m *Model) handleOverlayWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	if m.mode == ModeThemes {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			model, command := m.handleThemeKeys(keyMessage("up"))
			return model, command, true
		case tea.MouseButtonWheelDown:
			model, command := m.handleThemeKeys(keyMessage("down"))
			return model, command, true
		}
	}
	if m.mode == ModeHelp {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.helpOffset = max(0, m.helpOffset-1)
			return m, nil, true
		case tea.MouseButtonWheelDown:
			m.helpOffset = min(m.maxHelpOffset(), m.helpOffset+1)
			return m, nil, true
		}
	}
	return m, nil, false
}

func (m *Model) handleThemeMouseClick(rendered string, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	for index, name := range ThemeNames() {
		theme, _ := LookupTheme(name)
		if renderedTextHit(rendered, msg.X, msg.Y, theme.DisplayName) {
			m.themeCursor = index
			m.themeUseProject = false
			m.previewThemePicker()
			return m, nil
		}
	}
	return m.handleMouseKeyBindings(rendered, msg, []mouseKeyBinding{
		{label: "[p] Theme: Project / Selected", key: "p"},
		{label: "[a] Accent: Project / Theme default", key: "a"},
		{label: "[b] Background: Terminal / Theme", key: "b"},
		{label: "[m] Mode: Auto / Dark / Light", key: "m"},
		{label: "[Enter] Save globally", key: "enter"},
		{label: "[c] Save to project", key: "c"},
		{label: "[Esc] Cancel", key: "esc"},
	}, m.handleThemeKeys)
}

func (m *Model) closeOverlayOnClick(rendered string, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if renderedTextHit(rendered, msg.X, msg.Y, "[Esc] Close") {
		m.mode = ModeNormal
	}
	return m, nil
}

type mouseKeyHandler func(tea.KeyMsg) (tea.Model, tea.Cmd)

func (m *Model) handleMouseKeyBindings(
	rendered string,
	msg tea.MouseMsg,
	bindings []mouseKeyBinding,
	handler mouseKeyHandler,
) (tea.Model, tea.Cmd) {
	model, command, _ := m.handleMouseKeyBindingsHandled(rendered, msg, bindings, handler)
	return model, command
}

func (m *Model) handleMouseKeyBindingsHandled(
	rendered string,
	msg tea.MouseMsg,
	bindings []mouseKeyBinding,
	handler mouseKeyHandler,
) (tea.Model, tea.Cmd, bool) {
	for _, binding := range bindings {
		if renderedTextHit(rendered, msg.X, msg.Y, binding.label) {
			model, command := handler(keyMessage(binding.key))
			return model, command, true
		}
	}
	return m, nil, false
}

func keyMessage(name string) tea.KeyMsg {
	switch name {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}
