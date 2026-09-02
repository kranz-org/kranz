package ui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

const configCheckInterval = time.Second

// Configuration hot reload. The stamping, debounce, and ApplyConfig work
// itself lives in internal/app; this file only reacts to the result on the
// Bubble Tea goroutine — reconciling focus, appearance, and notifications.

func (m *Model) reloadConfig(force bool) tea.Cmd {
	if m.operation != "" {
		return nil
	}
	now := time.Now()
	if !force && !m.lastConfigCheck.IsZero() && now.Sub(m.lastConfigCheck) < configCheckInterval {
		return nil
	}
	if !m.configReloadBusy.CompareAndSwap(false, true) {
		return nil
	}
	m.lastConfigCheck = now
	before := m.configGeneration
	return func() tea.Msg {
		defer m.configReloadBusy.Store(false)
		result, err := m.app.Reload(force)
		if err != nil {
			return configReloadMsg{err: err}
		}
		project := m.app.Project()
		if !force && project.Generation == before {
			return nil
		}
		return configReloadMsg{result: result, generation: project.Generation, changed: project.Generation != before}
	}
}

func (m *Model) handleConfigReload(msg configReloadMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.addNotification("config", "Reload failed: "+msg.err.Error(), config.LogError)
		return m, nil
	}
	if !msg.changed {
		return m, nil
	}
	m.configGeneration = msg.generation
	m.resetLogCaches()
	focusedName := ""
	if svc := m.FocusedService(); svc != nil {
		focusedName = svc.Name
	}
	m.cfg = m.app.Config()
	if m.focusedAction != nil {
		if _, exists := m.cfg.ResolveAction(*m.focusedAction); !exists {
			m.focusedAction = nil
		}
	}
	if m.focusedActionGroup != "" {
		if _, exists := m.cfg.ActionGroups[m.focusedActionGroup]; !exists {
			m.focusedActionGroup = ""
		}
	}
	m.refreshServices()
	for index, svc := range m.services {
		if svc.Name == focusedName {
			m.focused = index
			break
		}
	}
	if len(m.services) == 0 && m.focusedAction == nil && m.focusedActionGroup == "" && len(m.cfg.ActionGroups) > 0 {
		m.focusServiceListRow(0)
	}
	if target, ok := m.pinnedRunTarget(); ok {
		valid := false
		if target.Kind == app.RunKindService {
			_, valid = m.app.Service(target.Name)
		} else {
			_, valid = m.cfg.ResolveAction(target.Action)
		}
		if !valid {
			m.pinnedLog, m.pinnedTarget, m.pinnedRun = "", app.RunTarget{}, 0
			if m.panelFocus == panelPinnedLogs {
				m.panelFocus = panelLogs
			}
		}
	}
	// The theme picker holds choices that are not in any file yet — a typed
	// accent, a background owner, a colour mode. Re-previewing rebuilds them
	// against the reloaded config; applyEffectiveAppearance would recompute from
	// the config and the saved settings alone and silently drop the session's
	// work while the panel still reported it. applyDetectedBackground draws the
	// same distinction.
	if m.mode == ModeThemes {
		m.previewThemePicker()
	} else if err := m.applyEffectiveAppearance(); err != nil {
		m.addNotification("appearance", err.Error(), config.LogWarn)
	}
	message := fmt.Sprintf("Configuration reloaded: %d added, %d removed, %d updated, %d restarted",
		len(msg.result.Added), len(msg.result.Removed), len(msg.result.Updated), len(msg.result.Restarted))
	m.addNotification("config", message, config.LogInfo)
	return m, m.scanFocusedPorts(true)
}
