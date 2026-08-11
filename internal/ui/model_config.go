package ui

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/config"
)

// Configuration hot reload. Edits are reconciled into the running manager, and
// an invalid file leaves the last known good runtime untouched.

func (m *Model) reloadConfig(force bool) tea.Cmd {
	if len(m.configPaths) == 0 || m.reloadBusy || m.operation != "" {
		return nil
	}
	if !force && time.Since(m.lastConfigScan) < time.Second {
		return nil
	}
	m.lastConfigScan = time.Now()
	m.reloadBusy = true
	paths := append([]string(nil), m.configPaths...)
	watchPaths := append([]string(nil), m.configWatchPaths...)
	previous := cloneConfigStamps(m.configStamps)
	return func() tea.Msg {
		stamps, err := readConfigStamps(watchPaths)
		if err != nil {
			return configReloadMsg{stamps: stamps, err: err}
		}
		changed := force || !equalConfigStamps(previous, stamps)
		if !changed {
			return configReloadMsg{stamps: stamps}
		}
		cfg, err := config.LoadFiles(paths)
		return configReloadMsg{cfg: cfg, stamps: stamps, err: err, changed: true}
	}
}

func (m *Model) handleConfigReload(msg configReloadMsg) (tea.Model, tea.Cmd) {
	m.reloadBusy = false
	if msg.stamps != nil {
		m.configStamps = msg.stamps
	}
	if msg.err != nil {
		m.addNotification("config", "Reload failed: "+msg.err.Error(), config.LogError)
		return m, nil
	}
	if !msg.changed || msg.cfg == nil {
		return m, nil
	}
	focusedName := ""
	if svc := m.FocusedService(); svc != nil {
		focusedName = svc.Name
	}
	result, err := m.manager.ApplyConfig(msg.cfg)
	if err != nil {
		m.addNotification("config", "Reload failed: "+err.Error(), config.LogError)
		return m, nil
	}
	m.cfg = msg.cfg
	if m.focusedAction != nil {
		if _, exists := msg.cfg.ResolveAction(*m.focusedAction); !exists {
			m.focusedAction = nil
		}
	}
	if m.focusedActionGroup != "" {
		if _, exists := msg.cfg.ActionGroups[m.focusedActionGroup]; !exists {
			m.focusedActionGroup = ""
		}
	}
	m.configWatchPaths = watchedConfigPaths(m.configPaths, msg.cfg.WatchPaths)
	if stamps, stampErr := readConfigStamps(m.configWatchPaths); stampErr == nil {
		m.configStamps = stamps
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
	if m.PinnedService() == nil {
		m.pinnedLog = ""
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
		len(result.Added), len(result.Removed), len(result.Updated), len(result.Restarted))
	m.addNotification("config", message, config.LogInfo)
	return m, m.scanFocusedPorts(true)
}

func readConfigStamps(paths []string) (map[string]configStamp, error) {
	result := make(map[string]configStamp, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			result[path] = configStamp{}
			continue
		}
		if err != nil {
			return result, fmt.Errorf("stat %s: %w", path, err)
		}
		result[path] = configStamp{Modified: info.ModTime().UnixNano(), Size: info.Size()}
	}
	return result, nil
}

func watchedConfigPaths(configPaths, auxiliaryPaths []string) []string {
	result := append([]string(nil), configPaths...)
	seen := make(map[string]bool, len(result)+len(auxiliaryPaths))
	for _, path := range result {
		seen[path] = true
	}
	for _, path := range auxiliaryPaths {
		if path != "" && !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}

func cloneConfigStamps(source map[string]configStamp) map[string]configStamp {
	result := make(map[string]configStamp, len(source))
	for path, stamp := range source {
		result[path] = stamp
	}
	return result
}

func equalConfigStamps(left, right map[string]configStamp) bool {
	if len(left) != len(right) {
		return false
	}
	for path, stamp := range left {
		if right[path] != stamp {
			return false
		}
	}
	return true
}
