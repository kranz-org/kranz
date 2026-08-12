package ui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/config"
)

// Key routing. Mode decides which handler sees a key first; within the
// dashboard the handlers are tried in order and the first to claim it wins.

func (m *Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m.beginShutdown()
	}
	if key.Matches(msg, m.keys.Shell) {
		return m, m.openCommandShell()
	}
	// Search is a text-entry mode and must preserve the user's actual runes.
	// Everywhere else, shortcuts follow their documented physical Latin keys.
	if m.mode != ModeSearch {
		msg = normalizeShortcutKey(msg)
	}

	switch m.mode {
	case ModeNormal:
		return m.handleNormalKeys(msg)
	case ModeSearch:
		return m.handleSearchKeys(msg)
	case ModeHelp:
		return m.handleHelpKeys(msg)
	case ModeConfirmQuit:
		return m.handleConfirmQuitKeys(msg)
	case ModeConfirmRestart:
		return m.handleConfirmRestartKeys(msg)
	case ModeConfirmClearLogs:
		return m.handleConfirmClearLogsKeys(msg)
	case ModeConfirmAction:
		return m.handleConfirmActionKeys(msg)
	case ModeConfirmServiceStart:
		return m.handleConfirmServiceStartKeys(msg)
	case ModeConfirmServiceStop:
		return m.handleConfirmServiceStopKeys(msg)
	case ModeConfirmThemeSave:
		return m.handleConfirmThemeSaveKeys(msg)
	case ModePortConflict:
		return m.handlePortConflictKeys(msg)
	case ModeThemes:
		return m.handleThemeKeys(msg)
	default:
		if msg.String() == "esc" || msg.String() == "q" {
			m.mode = ModeNormal
		}
		return m, nil
	}
}

func (m *Model) handleNormalKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Reload) {
		return m, tea.Batch(m.reloadConfig(true), m.probeTerminalBackground(true))
	}
	if key.Matches(msg, m.keys.Open) && m.panelFocus == panelServices {
		if m.listMode == listTags {
			m.toggleFocusedTagExpansion()
			return m, nil
		}
		if command, handled := m.openFocusedListItem(); handled {
			return m, command
		}
	}
	if m.handleNavigationKey(msg) {
		return m, nil
	}
	if model, command, handled := m.handleLifecycleKey(msg); handled {
		return model, command
	}
	if m.handleSearchNavigationKey(msg) {
		return m, nil
	}
	if m.handleViewKey(msg) {
		return m, nil
	}
	if key.Matches(msg, m.keys.Search) {
		return m, m.openSearchEditor()
	}
	if m.handleLogKey(msg) {
		return m, nil
	}
	if key.Matches(msg, m.keys.Quit) {
		if m.manager.HasRunningServices() || m.operation != "" {
			m.mode = ModeConfirmQuit
			return m, nil
		}
		return m.beginShutdown()
	}
	return m, nil
}

func (m *Model) handleNavigationKey(msg tea.KeyMsg) bool {
	switch {
	case key.Matches(msg, m.keys.FocusList):
		if m.panelFocus == panelServices {
			m.toggleListMode()
		} else {
			m.panelFocus = panelServices
		}
		return true
	case key.Matches(msg, m.keys.FocusDetails):
		m.panelFocus = panelDetails
		return true
	case key.Matches(msg, m.keys.FocusLogs):
		if m.PinnedService() != nil && m.panelFocus == panelLogs {
			m.panelFocus = panelPinnedLogs
		} else {
			m.panelFocus = panelLogs
		}
		return true
	case key.Matches(msg, m.keys.NextPanel):
		m.cyclePanelFocus(1)
		return true
	case key.Matches(msg, m.keys.PreviousPanel):
		m.cyclePanelFocus(-1)
		return true
	case key.Matches(msg, m.keys.Up):
		m.movePanelCursor(-1)
		return true
	case key.Matches(msg, m.keys.Down):
		m.movePanelCursor(1)
		return true
	case key.Matches(msg, m.keys.Left), key.Matches(msg, m.keys.Right):
		if m.panelFocus != panelServices {
			return false
		}
		m.toggleListMode()
		return true
	default:
		return false
	}
}

func (m *Model) handleViewKey(msg tea.KeyMsg) bool {
	switch {
	case key.Matches(msg, m.keys.Tags):
		m.toggleListMode()
		m.panelFocus = panelServices
		return true
	case key.Matches(msg, m.keys.ResetTags):
		m.selectedTags = nil
		return true
	case key.Matches(msg, m.keys.Health):
		m.mode = ModeHealthHistory
		return true
	case key.Matches(msg, m.keys.Notifs):
		m.mode = ModeNotifications
		return true
	case key.Matches(msg, m.keys.Help):
		m.helpOffset = 0
		m.mode = ModeHelp
		return true
	case key.Matches(msg, m.keys.PinLogs):
		m.togglePinnedLog()
		return true
	case msg.String() == "ctrl+t":
		m.openThemePicker()
		return true
	default:
		return false
	}
}

func (m *Model) handleHelpKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.helpOffset = max(0, m.helpOffset-1)
	case key.Matches(msg, m.keys.Down):
		m.helpOffset = min(m.maxHelpOffset(), m.helpOffset+1)
	case msg.String() == "esc", msg.String() == "q", msg.String() == "?":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) handleLogKey(msg tea.KeyMsg) bool {
	switch {
	case key.Matches(msg, m.keys.ClearSearch):
		// Esc is the second step out of search: the editor exit keeps the
		// filter, and this drops it. Without a pattern the key stays inert.
		if m.logSearcher == nil || !m.logSearcher.HasPattern() {
			return false
		}
		m.clearSearch()
		return true
	case key.Matches(msg, m.keys.WrapLogs):
		m.wrapLogs = !m.wrapLogs
		m.logOffset = 0
		m.logAnchor = 0
		m.followMode = true
		m.pinnedOffset = 0
		m.pinnedAnchor = 0
		m.pinnedFollow = true
		m.logPaused = false
		state := "disabled"
		if m.wrapLogs {
			state = "enabled"
		}
		m.addNotification("logs", "Line wrapping "+state, config.LogInfo)
		return true
	case key.Matches(msg, m.keys.LogTime):
		m.showLogTime = !m.showLogTime
		m.logOffset = 0
		m.logAnchor = 0
		m.followMode = true
		m.pinnedOffset = 0
		m.pinnedAnchor = 0
		m.pinnedFollow = true
		m.logPaused = false
		state := "hidden"
		if m.showLogTime {
			state = "shown"
		}
		m.addNotification("logs", "Log timestamps "+state, config.LogInfo)
		return true
	case key.Matches(msg, m.keys.Freeze):
		if !m.followMode {
			m.followMode = true
			m.logPaused = false
			m.logOffset = 0
			m.logAnchor = 0
		} else {
			m.followMode = false
			m.logPaused = true
			m.logAnchor = m.displayedLogLineCount()
		}
		return true
	case key.Matches(msg, m.keys.Clear):
		return m.beginClearLogs()
	default:
		return false
	}
}

func (m *Model) triggerAction(action string) (tea.Model, tea.Cmd) {
	switch action {
	case "toggle":
		return m.toggleSelectedServices()
	case "force":
		return m.forceToggleSelectedServices()
	case "select":
		m.toggleFocusedSelection()
		return m, nil
	case "restart":
		return m.restartSelectedService()
	case "all":
		m.toggleAllSelection()
		return m, nil
	case "quit":
		if m.manager.HasRunningServices() || m.operation != "" {
			m.mode = ModeConfirmQuit
			return m, nil
		}
		return m.beginShutdown()
	default:
		return m, nil
	}
}
