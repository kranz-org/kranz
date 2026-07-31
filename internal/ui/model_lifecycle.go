package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

// Service lifecycle from the dashboard: resolving what a key targets, running
// the operation, and the confirmations that guard the destructive ones.

// Shutdown is the idempotent cleanup boundary for every application exit path.
func (m *Model) Shutdown() error {
	m.shutdownOnce.Do(func() {
		if m.operationCancel != nil {
			m.operationCancel()
		}
		m.shutdownErr = m.manager.Shutdown()
		m.healthChecker.StopAll()
	})
	return m.shutdownErr
}

func (m *Model) handleLifecycleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch {
	case key.Matches(msg, m.keys.Select):
		m.toggleCurrentSelection()
		return m, nil, true
	case key.Matches(msg, m.keys.ForceStart):
		model, command := m.forceToggleSelectedServices()
		return model, command, true
	case key.Matches(msg, m.keys.Toggle):
		model, command := m.toggleSelectedServices()
		return model, command, true
	case key.Matches(msg, m.keys.StartAll):
		m.toggleAllSelection()
		return m, nil, true
	case key.Matches(msg, m.keys.StopAll):
		m.cancelStartOperation()
		model, command := m.beginOperation(operationStopAll, "all services", "Stopping all services", m.manager.StopAll)
		return model, command, true
	case key.Matches(msg, m.keys.Restart):
		model, command := m.restartSelectedService()
		return model, command, true
	case key.Matches(msg, m.keys.RestartAll):
		model, command := m.beginOperation(operationRestartAll, "running services", "Restarting services", m.manager.RestartAll)
		return model, command, true
	default:
		return m, nil, false
	}
}

func (m *Model) beginClearLogs() bool {
	var svc *service.Service
	switch m.panelFocus {
	case panelLogs:
		svc = m.FocusedService()
	case panelPinnedLogs:
		svc = m.PinnedService()
	default:
		return false
	}
	if svc == nil {
		return false
	}
	m.mode = ModeConfirmClearLogs
	m.clearTarget = svc.Name
	m.clearPinned = m.panelFocus == panelPinnedLogs
	return true
}

func (m *Model) clearConfirmedLogs() {
	svc, ok := m.manager.GetService(m.clearTarget)
	if ok {
		svc.ClearLogs()
		svc.ResetNewLogCount()
		if focused := m.FocusedService(); focused != nil && focused.Name == svc.Name {
			m.logOffset, m.logAnchor, m.followMode, m.logPaused = 0, 0, true, false
			m.currentMatch = -1
		}
		if pinned := m.PinnedService(); pinned != nil && pinned.Name == svc.Name {
			m.pinnedOffset, m.pinnedAnchor, m.pinnedFollow = 0, 0, true
		}
		m.addNotification(svc.Name, "Logs cleared", config.LogInfo)
	}
	m.clearTarget = ""
	m.clearPinned = false
	m.mode = ModeNormal
}

func (m *Model) startSelectedService() (tea.Model, tea.Cmd) {
	svc := m.FocusedService()
	if svc == nil {
		return m, nil
	}
	if svc.Status() != config.StatusStopped {
		m.addNotification(svc.Name, "Service is already running. Press s to stop it.", config.LogInfo)
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return m.beginCancelableOperation(operationStart, svc.Name, "Starting "+svc.Name, cancel, func() error {
		return m.manager.StartServicesContext(ctx, []string{svc.Name})
	})
}

func (m *Model) selectedTargetNames() []string {
	selectedTags := m.selectedTags
	if len(selectedTags) == 0 && len(m.selected) == 0 && m.listMode == listTags {
		if row, ok := m.focusedTagRow(); ok {
			if row.Service != nil {
				return []string{row.Service.Name}
			}
			selectedTags = []string{row.Tag}
		}
	}
	if len(selectedTags) > 0 {
		matches := make(map[string]bool)
		for _, name := range m.cfg.GetServicesByTags(selectedTags) {
			matches[name] = true
		}
		names := make([]string, 0, len(matches))
		for _, svc := range m.allServices {
			if matches[svc.Name] {
				names = append(names, svc.Name)
			}
		}
		return names
	}
	if len(m.selected) == 0 {
		if svc := m.FocusedService(); svc != nil {
			return []string{svc.Name}
		}
		return nil
	}
	names := make([]string, 0, len(m.selected))
	for _, svc := range m.allServices {
		if m.selected[svc.Name] {
			names = append(names, svc.Name)
		}
	}
	return names
}

func (m *Model) selectedTargetLabel(names []string) string {
	if len(m.selectedTags) == 1 {
		return "tag " + m.selectedTags[0]
	}
	if len(m.selectedTags) > 1 {
		return fmt.Sprintf("%d selected tags", len(m.selectedTags))
	}
	if m.listMode == listTags && len(m.selectedTags) == 0 && len(m.selected) == 0 {
		if row, ok := m.focusedTagRow(); ok {
			if row.Service != nil {
				return row.Service.Name
			}
			return "tag " + row.Tag
		}
	}
	if len(names) > 1 {
		return fmt.Sprintf("%d selected services", len(names))
	}
	return names[0]
}

func (m *Model) toggleSelectedServices() (tea.Model, tea.Cmd) {
	names := m.selectedTargetNames()
	if len(names) == 0 {
		return m, nil
	}

	allActive := true
	for _, name := range names {
		svc, ok := m.manager.GetService(name)
		if !ok || !serviceStartPlanned(svc) {
			allActive = false
			break
		}
	}
	target := m.selectedTargetLabel(names)
	if allActive {
		m.cancelStartOperation()
		return m.beginOperation(operationStopSet, target, "Stopping "+target, func() error {
			return m.manager.StopServices(names)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	return m.beginCancelableOperation(operationStartSet, target, "Starting "+target, cancel, func() error {
		return m.manager.StartServicesContext(ctx, names)
	})
}

func serviceStartPlanned(svc *service.Service) bool {
	return svc.Status() != config.StatusStopped || svc.DesiredRunning()
}

func (m *Model) forceToggleSelectedServices() (tea.Model, tea.Cmd) {
	names := m.selectedTargetNames()
	if len(names) == 0 {
		return m, nil
	}
	// Shift+S is also an escape hatch from an in-flight dependency gate. The
	// stale dependency-aware result is ignored because beginOperation advances
	// the operation ID before starting the direct targets.
	m.cancelStartOperation()
	target := m.selectedTargetLabel(names)
	allRunning := true
	for _, name := range names {
		svc, ok := m.manager.GetService(name)
		if !ok || svc.Status() == config.StatusStopped {
			allRunning = false
			break
		}
	}
	if allRunning {
		return m.beginOperation(operationForceStop, target, "Force stopping "+target, func() error {
			return m.manager.ForceStopServices(names)
		})
	}
	return m.beginOperation(operationForceStart, target, "Force starting "+target, func() error {
		return m.manager.ForceStartServices(names)
	})
}

func (m *Model) restartSelectedService() (tea.Model, tea.Cmd) {
	svc := m.FocusedService()
	if svc == nil {
		return m, nil
	}
	if svc.Status() == config.StatusStopped {
		return m.startSelectedService()
	}
	affected := m.manager.GetAffectedServices(svc.Name)
	if len(affected) > 1 {
		m.mode = ModeConfirmRestart
		m.confirmTarget = svc.Name
		m.confirmAction = strings.Join(affected[1:], ", ")
		return m, nil
	}
	return m.beginRestart(svc.Name)
}

func (m *Model) beginRestart(name string) (tea.Model, tea.Cmd) {
	m.mode = ModeNormal
	return m.beginOperation(operationRestart, name, "Restarting "+name, func() error {
		return m.manager.RestartService(name)
	})
}

func (m *Model) beginOperation(kind operationKind, target, label string, operation func() error) (tea.Model, tea.Cmd) {
	return m.beginCancelableOperation(kind, target, label, nil, operation)
}

func (m *Model) beginCancelableOperation(kind operationKind, target, label string, cancel context.CancelFunc, operation func() error) (tea.Model, tea.Cmd) {
	if m.operation != "" {
		if cancel != nil {
			cancel()
		}
		m.addNotification("system", "Wait for the current operation: "+m.operation, config.LogWarn)
		return m, nil
	}
	m.operation = label
	m.operationKind = kind
	m.operationCancel = cancel
	m.operationID++
	operationID := m.operationID
	return m, func() tea.Msg {
		return operationResultMsg{id: operationID, kind: kind, target: target, err: operation()}
	}
}

func (m *Model) cancelStartOperation() {
	switch m.operationKind {
	case operationStart, operationStartSet:
		if m.operationCancel != nil {
			m.operationCancel()
		}
		m.operation = ""
		m.operationKind = ""
		m.operationCancel = nil
	}
}

func (m *Model) handleOperationResult(msg operationResultMsg) (tea.Model, tea.Cmd) {
	if msg.id != m.operationID {
		return m, nil
	}
	m.operation = ""
	m.operationKind = ""
	m.operationCancel = nil
	if msg.err != nil {
		var conflict *service.PortConflictError
		if errors.As(msg.err, &conflict) {
			m.conflictService = conflict.Service
			if m.conflictService == "" {
				m.conflictService = msg.target
			}
			m.conflictPorts = map[int]*config.PortInfo{
				conflict.Port: {
					Port:    conflict.Port,
					PID:     conflict.PID,
					Process: conflict.Process,
					Command: conflict.Command,
				},
			}
			m.conflictOwner = conflict.OwnerService
			m.conflictExternal = conflict.External
			m.mode = ModePortConflict
			return m, nil
		}
		m.addNotification(msg.target, msg.err.Error(), config.LogError)
		return m, nil
	}

	message := map[operationKind]string{
		operationStart:      "Service started",
		operationStartSet:   "Selection started (required dependencies included)",
		operationForceStart: "Selected services started without dependencies",
		operationForceStop:  "Selected services stopped without stopping dependents",
		operationStopAll:    "All services have been stopped",
		operationStopSet:    "Selection and dependent services stopped; ports released",
		operationRestart:    "Service restarted",
		operationRestartAll: "Running services have been restarted",
	}[msg.kind]
	m.addNotification(msg.target, message, config.LogInfo)
	m.portService = ""
	m.portChecked = time.Time{}
	m.portScanBusy = false
	return m, m.scanFocusedPorts(true)
}

func (m *Model) beginShutdown() (tea.Model, tea.Cmd) {
	if m.operationCancel != nil {
		m.operationCancel()
		m.operationCancel = nil
	}
	m.operationID++
	m.operationKind = ""
	if m.operation != "Shutting down" {
		m.operation = "Shutting down"
	}
	m.mode = ModeNormal
	return m, func() tea.Msg { return shutdownResultMsg{err: m.Shutdown()} }
}

func (m *Model) handleConfirmQuitKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.beginShutdown()
	case "n", "N", "esc":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) handleConfirmRestartKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.beginRestart(m.confirmTarget)
	case "n", "N", "esc":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) handleConfirmClearLogsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.clearConfirmedLogs()
	case "esc":
		m.clearTarget = ""
		m.clearPinned = false
		m.mode = ModeNormal
	}
	return m, nil
}
