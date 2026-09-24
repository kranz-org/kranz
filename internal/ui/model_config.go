package ui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

const configCheckInterval = time.Second

// Configuration changes are watched passively. Applying them is an explicit
// Ctrl+L action, so a lifecycle key cannot become a reload choice on its own.

func (m *Model) checkConfigChanges() tea.Cmd {
	now := time.Now()
	if !m.lastConfigCheck.IsZero() && now.Sub(m.lastConfigCheck) < configCheckInterval {
		return nil
	}
	if !m.configReloadBusy.CompareAndSwap(false, true) {
		return nil
	}
	m.lastConfigCheck = now
	application, sessionGen, generation := m.app, m.sessionGeneration, m.configGeneration
	release := retainRuntimeApplication(application)
	return func() tea.Msg {
		defer release()
		defer m.configReloadBusy.Store(false)
		changed, err := application.ConfigChanged()
		return configChangedMsg{changed: changed, err: err, baseGeneration: generation,
			projectGeneration: application.Project().Generation, sessionGen: sessionGen}
	}
}

func (m *Model) resumeQueuedConfigReload() tea.Cmd {
	if !m.reloadRequested {
		return nil
	}
	m.reloadRequested = false
	return m.reloadConfig(true)
}

func (m *Model) reloadConfig(force bool) tea.Cmd {
	if m.operation != "" || m.mode == ModeConfirmConfigReload {
		return nil
	}
	if !m.configReloadBusy.CompareAndSwap(false, true) {
		m.reloadRequested = true
		return nil
	}
	before := m.configGeneration
	application, sessionGen := m.app, m.sessionGeneration
	release := retainRuntimeApplication(application)
	return func() tea.Msg {
		defer release()
		defer m.configReloadBusy.Store(false)
		fingerprint, removed, previewErr := previewConfigReload(application)
		if previewErr != nil {
			return configReloadMsg{err: previewErr, sessionGen: sessionGen}
		}
		if len(removed) > 0 {
			return configReloadPreviewMsg{removed: removed, fingerprint: fingerprint, sessionGen: sessionGen}
		}
		result, err := application.Reload(force)
		if err != nil {
			return configReloadMsg{err: err, sessionGen: sessionGen}
		}
		project := application.Project()
		if !force && project.Generation == before {
			return nil
		}
		return configReloadMsg{result: result, generation: project.Generation, changed: project.Generation != before, sessionGen: sessionGen}
	}
}

func previewConfigReload(application app.API) (string, []string, error) {
	request := application.ProjectComposition()
	if !request.Configured() {
		return "", nil, nil
	}
	next, err := config.Compose(config.LoadOptions{
		Directory: request.Directory, Sources: append([]string(nil), request.Sources...),
		Overrides: append([]string(nil), request.Overrides...), FollowSymlinks: request.FollowSymlinks,
	})
	if err != nil {
		return "", nil, err
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return "", nil, err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(encoded))
	retained := make(map[string]bool, len(next.Services))
	retainedNames := make(map[string]bool, len(next.Services))
	for name := range next.Services {
		retainedNames[name] = true
		identity := next.ServiceMetadata[name].ID
		if identity == "" {
			identity = name
		}
		retained[identity] = true
	}
	var removed []string
	for _, svc := range application.Services() {
		identity := svc.ID
		if identity == "" {
			identity = svc.Name
		}
		if reloadServiceActive(svc) && !retained[identity] && (identity != svc.Name || !retainedNames[svc.Name]) {
			removed = append(removed, svc.Name)
		}
	}
	sort.Strings(removed)
	return fingerprint, removed, nil
}

func reloadServiceActive(svc *app.ServiceSnapshot) bool {
	return svc.DesiredRunning || svc.State.Status == config.StatusRunning || svc.State.Status == config.StatusUnhealthy ||
		svc.State.Status == config.StatusStarting || svc.State.Status == config.StatusStopping
}

func (m *Model) handleConfigReloadPreview(msg configReloadPreviewMsg) (tea.Model, tea.Cmd) {
	m.pendingReloadRemoved = append([]string(nil), msg.removed...)
	m.pendingReloadFingerprint = msg.fingerprint
	m.mode = ModeConfirmConfigReload
	return m, nil
}

func (m *Model) handleConfirmConfigReloadKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "s", "S":
		return m, m.completeConfigReload(true)
	case "k", "K":
		return m, m.completeConfigReload(false)
	case "esc", "n", "N":
		m.pendingReloadRemoved = nil
		m.pendingReloadFingerprint = ""
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) completeConfigReload(stopRemoved bool) tea.Cmd {
	if !m.configReloadBusy.CompareAndSwap(false, true) {
		return nil
	}
	names := append([]string(nil), m.pendingReloadRemoved...)
	fingerprint := m.pendingReloadFingerprint
	m.pendingReloadRemoved = nil
	m.pendingReloadFingerprint = ""
	m.mode = ModeNormal
	application, sessionGen := m.app, m.sessionGeneration
	before := m.configGeneration
	release := retainRuntimeApplication(application)
	return func() tea.Msg {
		defer release()
		defer m.configReloadBusy.Store(false)
		currentFingerprint, currentRemoved, err := previewConfigReload(application)
		if err != nil {
			return configReloadMsg{err: err, sessionGen: sessionGen}
		}
		if currentFingerprint != fingerprint {
			if len(currentRemoved) > 0 {
				return configReloadPreviewMsg{removed: currentRemoved, fingerprint: currentFingerprint, sessionGen: sessionGen}
			}
			names = nil
		}
		if stopRemoved && len(names) > 0 {
			for _, name := range names {
				if svc, exists := application.Service(name); exists && reloadServiceActive(svc) && !svc.CanStop {
					return configReloadMsg{err: fmt.Errorf("cannot stop %q; choose Keep running or Cancel", name), sessionGen: sessionGen}
				}
			}
			if err := application.ForceStopServices(names); err != nil {
				return configReloadMsg{err: err, sessionGen: sessionGen}
			}
			for _, name := range names {
				if svc, exists := application.Service(name); exists && reloadServiceActive(svc) {
					return configReloadMsg{err: fmt.Errorf("service %q is still running; configuration was not reloaded", name), sessionGen: sessionGen}
				}
			}
		}
		result, err := application.Reload(true)
		if err != nil {
			return configReloadMsg{err: err, sessionGen: sessionGen}
		}
		project := application.Project()
		return configReloadMsg{result: result, generation: project.Generation, changed: project.Generation != before, sessionGen: sessionGen}
	}
}

func (m *Model) handleConfigReload(msg configReloadMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.addNotification("config", "Reload failed: "+msg.err.Error(), config.LogError)
		return m, nil
	}
	m.configChanged = false
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
			for _, svc := range m.allServices {
				if svc.Name == target.Name {
					valid = true
					break
				}
			}
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
	message := "Configuration applied by another client"
	if !msg.external {
		message = fmt.Sprintf("Configuration reloaded: %d added, %d removed, %d updated, %d pending restart",
			len(msg.result.Added), len(msg.result.Removed), len(msg.result.Updated), len(msg.result.Pending))
	}
	m.addNotification("config", message, config.LogInfo)
	for _, pending := range msg.result.Pending {
		detail := pending.Name
		if pending.DesiredName != "" && pending.DesiredName != pending.Name {
			detail += " -> " + pending.DesiredName
		}
		m.addNotification("config", fmt.Sprintf("Pending %s for %s: %s", pending.Kind, detail, pending.Reason), config.LogWarn)
	}
	return m, m.scanFocusedPorts(true)
}
