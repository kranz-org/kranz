package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// Service lifecycle from the dashboard: resolving what a key targets, running
// the operation, and the confirmations that guard the destructive ones.

// Shutdown is the idempotent cleanup boundary for every application exit path.
func (m *Model) Shutdown() error {
	m.shutdownOnce.Do(func() {
		waitStoppedLogCommands(m.stopLogCommands())
		m.abortAllOperations()
		if !m.detachOnExit {
			m.shutdownErr = m.app.Shutdown()
		}
	})
	return m.shutdownErr
}

func (m *Model) handleLifecycleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if m.listMode == listServices && (m.focusedAction != nil || m.focusedActionGroup != "") {
		// Space marks a value the way it selects a service elsewhere: it is the
		// "pick this one" key, and inside a parameter tree the values are what
		// there is to pick.
		if key.Matches(msg, m.keys.Select) && m.markFocusedParamRow() {
			return m, nil, true
		}
		if key.Matches(msg, m.keys.Toggle) && m.focusedAction != nil && !m.paramRowFocused() {
			command, handled := m.toggleFocusedAction()
			return m, command, handled
		}
		// A parameter row owns none of the lifecycle keys: it is a setting of
		// an action, not a service to start.
		if key.Matches(msg, m.keys.Clear) && m.focusedAction != nil && m.actionHasParams(*m.focusedAction) {
			return m, m.openParamForm(*m.focusedAction), true
		}
		if key.Matches(msg, m.keys.Disable) && m.paramRowFocused() && !m.focusedParam.IsValue {
			m.toggleParamDisabled(m.focusedParam.ID, m.focusedParam.Name)
			return m, nil, true
		}
		if key.Matches(msg, m.keys.Select) || key.Matches(msg, m.keys.ForceStart) || key.Matches(msg, m.keys.Toggle) || key.Matches(msg, m.keys.Restart) {
			return m, nil, true
		}
	}
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
		names := m.cfg.ServiceNames()
		if m.requiresStopConfirmation(names) {
			model, command := m.beginServiceStopConfirmation(names, "all services", false)
			m.pendingStopAll = true
			return model, command, true
		}
		model, command := m.beginOperation(operationStopAll, "all services", "Stopping all services", nil, m.app.StopAll)
		return model, command, true
	case key.Matches(msg, m.keys.Restart):
		model, command := m.restartSelectedService()
		return model, command, true
	case key.Matches(msg, m.keys.RestartAll):
		if m.app.HasRunningServices() {
			m.mode = ModeConfirmRestart
			m.confirmTarget = "running services"
			m.confirmAction = ""
			m.confirmRestartAll = true
			return m, nil, true
		}
		model, command := m.beginOperation(operationRestartAll, "running services", "Restarting services", nil, m.app.RestartAll)
		return model, command, true
	default:
		return m, nil, false
	}
}

func (m *Model) beginClearLogs() bool {
	if m.panelFocus == panelLogs && m.focusedAction != nil {
		id := *m.focusedAction
		m.clearAction, m.clearTarget, m.clearPinned = &id, runTargetLabel(app.ActionRunTarget(id)), false
		m.mode = ModeConfirmClearLogs
		return true
	}
	if m.panelFocus == panelPinnedLogs {
		if target, ok := m.pinnedRunTarget(); ok && target.Kind == app.RunKindAction {
			id := target.Action
			m.clearAction, m.clearTarget, m.clearPinned = &id, runTargetLabel(target), true
			m.mode = ModeConfirmClearLogs
			return true
		}
	}
	var svc *app.ServiceSnapshot
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
	if m.clearAction != nil {
		target := app.ActionRunTarget(*m.clearAction)
		m.app.ClearActionLogs(*m.clearAction)
		m.invalidateLogCache(target)
		m.addNotification(runTargetLabel(app.ActionRunTarget(*m.clearAction)), "Action logs cleared", config.LogInfo)
		m.clearAction = nil
		m.clearTarget = ""
		m.clearPinned = false
		m.mode = ModeNormal
		return
	}
	svc, ok := m.app.Service(m.clearTarget)
	if ok {
		m.app.ClearLogs(svc.Name)
		m.invalidateLogCache(app.ServiceRunTarget(svc.Name))
		svc.State.NewLogCount = 0
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
	m.clearAction = nil
	m.clearPinned = false
	m.mode = ModeNormal
}

func (m *Model) startSelectedService() (tea.Model, tea.Cmd) {
	svc := m.FocusedService()
	if svc == nil {
		return m, nil
	}
	if !svc.CanStart {
		m.addNotification(svc.Name, "Service is already running. Press s to stop it.", config.LogInfo)
		return m, nil
	}
	if m.requiresStartConfirmation([]string{svc.Name}, true) {
		return m.beginServiceStartConfirmation([]string{svc.Name}, svc.Name, false)
	}
	ctx, cancel := context.WithCancel(context.Background())
	application := m.app
	return m.beginCancelableOperation(operationStart, svc.Name, "Starting "+svc.Name, []string{svc.Name}, cancel, func() error {
		return application.StartServicesContext(ctx, []string{svc.Name})
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
		svc, ok := m.app.Service(name)
		if !ok || !app.ServiceStartPlanned(svc) {
			allActive = false
			break
		}
	}
	target := m.selectedTargetLabel(names)
	if allActive {
		for _, name := range names {
			svc, ok := m.app.Service(name)
			if ok && !svc.CanStop {
				m.addNotification(name, "Service has no stop capability", config.LogWarn)
				return m, nil
			}
		}
		if m.requiresStopConfirmation(names) {
			return m.beginServiceStopConfirmation(names, target, false)
		}
		application := m.app
		return m.beginOperation(operationStopSet, target, "Stopping "+target, names, func() error {
			return application.StopServices(names)
		})
	}
	for _, name := range names {
		svc, ok := m.app.Service(name)
		if ok && !app.ServiceStartPlanned(svc) && !svc.CanStart {
			m.addNotification(name, "Service has no start capability", config.LogWarn)
			return m, nil
		}
	}
	if m.requiresStartConfirmation(names, true) {
		return m.beginServiceStartConfirmation(names, target, false)
	}
	ctx, cancel := context.WithCancel(context.Background())
	application := m.app
	return m.beginCancelableOperation(operationStartSet, target, "Starting "+target, names, cancel, func() error {
		return application.StartServicesContext(ctx, names)
	})
}

func (m *Model) forceToggleSelectedServices() (tea.Model, tea.Cmd) {
	names := m.selectedTargetNames()
	if len(names) == 0 {
		return m, nil
	}
	target := m.selectedTargetLabel(names)
	allRunning := true
	for _, name := range names {
		svc, ok := m.app.Service(name)
		if !ok || svc.CanStart {
			allRunning = false
			break
		}
	}
	if allRunning {
		for _, name := range names {
			svc, ok := m.app.Service(name)
			if ok && !svc.CanStop {
				m.addNotification(name, "Service has no stop capability", config.LogWarn)
				return m, nil
			}
		}
		if m.requiresStopConfirmation(names) {
			return m.beginServiceStopConfirmation(names, target, true)
		}
		application := m.app
		return m.beginOperation(operationForceStop, target, "Force stopping "+target, names, func() error {
			return application.ForceStopServices(names)
		})
	}
	for _, name := range names {
		svc, ok := m.app.Service(name)
		if ok && !app.ServiceStartPlanned(svc) && !svc.CanStart {
			m.addNotification(name, "Service has no start capability", config.LogWarn)
			return m, nil
		}
	}
	if m.requiresStartConfirmation(names, false) {
		return m.beginServiceStartConfirmation(names, target, true)
	}
	application := m.app
	return m.beginOperation(operationForceStart, target, "Force starting "+target, names, func() error {
		return application.ForceStartServices(names)
	})
}

func (m *Model) requiresStartConfirmation(names []string, includeDependencies bool) bool {
	return len(m.app.StartConfirmationNames(names, includeDependencies)) > 0
}

func (m *Model) beginServiceStartConfirmation(names []string, target string, force bool) (tea.Model, tea.Cmd) {
	m.pendingStartNames = append([]string(nil), names...)
	m.pendingStartTarget = target
	m.pendingStartForce = force
	m.mode = ModeConfirmServiceStart
	return m, nil
}

func (m *Model) confirmServiceStart() (tea.Model, tea.Cmd) {
	names := append([]string(nil), m.pendingStartNames...)
	target := m.pendingStartTarget
	force := m.pendingStartForce
	m.cancelServiceStartConfirmation()
	if force {
		application := m.app
		return m.beginOperation(operationForceStart, target, "Force starting "+target, names, func() error {
			return application.ForceStartServices(names)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	application := m.app
	return m.beginCancelableOperation(operationStartSet, target, "Starting "+target, names, cancel, func() error {
		return application.StartServicesContext(ctx, names)
	})
}

func (m *Model) cancelServiceStartConfirmation() {
	m.pendingStartNames = nil
	m.pendingStartTarget = ""
	m.pendingStartForce = false
	m.mode = ModeNormal
}

func (m *Model) handleConfirmServiceStartKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.confirmServiceStart()
	case "n", "N", "esc":
		m.cancelServiceStartConfirmation()
	}
	return m, nil
}

func (m *Model) restartSelectedService() (tea.Model, tea.Cmd) {
	svc := m.FocusedService()
	if svc == nil {
		return m, nil
	}
	if svc.CanStart {
		return m.startSelectedService()
	}
	affected := m.app.AffectedServices(svc.Name)
	m.mode = ModeConfirmRestart
	m.confirmTarget = svc.Name
	m.confirmRestartAll = false
	if len(affected) > 1 {
		m.confirmAction = strings.Join(affected[1:], ", ")
	} else {
		m.confirmAction = ""
	}
	return m, nil
}

func (m *Model) requiresStopConfirmation(names []string) bool {
	return m.app.RequiresStopConfirmation(names)
}

func (m *Model) beginServiceStopConfirmation(names []string, target string, force bool) (tea.Model, tea.Cmd) {
	m.pendingStopNames = append([]string(nil), names...)
	m.pendingStopTarget = target
	m.pendingStopForce = force
	m.mode = ModeConfirmServiceStop
	return m, nil
}

func (m *Model) confirmServiceStop() (tea.Model, tea.Cmd) {
	names := append([]string(nil), m.pendingStopNames...)
	target := m.pendingStopTarget
	force := m.pendingStopForce
	stopAll := m.pendingStopAll
	m.cancelServiceStopConfirmation()
	if stopAll {
		return m.beginOperation(operationStopAll, target, "Stopping all services", nil, m.app.StopAll)
	}
	if force {
		application := m.app
		return m.beginOperation(operationForceStop, target, "Force stopping "+target, names, func() error {
			return application.ForceStopServices(names)
		})
	}
	application := m.app
	return m.beginOperation(operationStopSet, target, "Stopping "+target, names, func() error {
		return application.StopServices(names)
	})
}

func (m *Model) cancelServiceStopConfirmation() {
	m.pendingStopNames = nil
	m.pendingStopTarget = ""
	m.pendingStopForce = false
	m.pendingStopAll = false
	m.mode = ModeNormal
}

func (m *Model) handleConfirmServiceStopKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.confirmServiceStop()
	case "n", "N", "esc":
		m.cancelServiceStopConfirmation()
	}
	return m, nil
}

// activeOperation is one in-flight lifecycle operation. The dashboard runs
// more than one at a time when their target service sets are disjoint, so it
// tracks each operation's identity, label, cancellation, and claimed services
// instead of a single scalar.
type activeOperation struct {
	id       int
	kind     operationKind
	label    string
	cancel   context.CancelFunc
	services map[string]bool
}

func (m *Model) beginRestart(name string) (tea.Model, tea.Cmd) {
	m.mode = ModeNormal
	application := m.app
	return m.beginOperation(operationRestart, name, "Restarting "+name, []string{name}, func() error {
		return application.RestartService(name)
	})
}

func (m *Model) beginOperation(kind operationKind, target, label string, services []string, operation func() error) (tea.Model, tea.Cmd) {
	return m.beginCancelableOperation(kind, target, label, services, nil, operation)
}

// beginCancelableOperation starts a lifecycle operation unless its affected
// services overlap an operation already in flight. Disjoint operations run
// concurrently, so starting an independent service (for example a standalone
// docs server) is never blocked behind another service's dependency gate.
// Force start and force stop are explicit overrides: they cancel whatever they
// overlap so the focused targets can change state immediately.
func (m *Model) beginCancelableOperation(kind operationKind, target, label string, services []string, cancel context.CancelFunc, operation func() error) (tea.Model, tea.Cmd) {
	affected := m.operationAffectedServices(kind, services)
	if overlapping := m.overlappingOperations(affected); len(overlapping) > 0 {
		if m.canInterruptOperations(kind, overlapping) {
			m.abortOperations(overlapping)
		} else {
			if cancel != nil {
				cancel()
			}
			m.addNotification("system", "Wait for the current operation: "+newestOperation(overlapping).label, config.LogWarn)
			return m, nil
		}
	}
	m.operationID++
	op := &activeOperation{id: m.operationID, kind: kind, label: label, cancel: cancel, services: affected}
	m.operations[op.id] = op
	m.refreshOperationMirror()
	operationID, sessionGen := op.id, m.sessionGeneration
	release := retainRuntimeApplication(m.app)
	return m, func() tea.Msg {
		defer release()
		return operationResultMsg{id: operationID, kind: kind, target: target, err: operation(), sessionGen: sessionGen}
	}
}

// operationAffectedServices is the set of services an operation may touch.
// Two operations whose sets do not intersect cannot race on the same service.
func (m *Model) operationAffectedServices(kind operationKind, names []string) map[string]bool {
	switch kind {
	case operationStart, operationStartSet:
		return m.dependencyClosure(names)
	case operationStopSet:
		return m.dependentClosure(names)
	case operationRestart:
		return m.restartAffectedServices(names)
	case operationForceStart, operationForceStop:
		return nameSet(names)
	case operationStopAll, operationRestartAll:
		return m.allServiceNames()
	default:
		return nameSet(names)
	}
}

// dependencyClosure expands names with every transitive dependency, mirroring
// the include-dependencies walk StartServicesContext performs.
func (m *Model) dependencyClosure(names []string) map[string]bool {
	closure := make(map[string]bool, len(names))
	var visit func(string)
	visit = func(name string) {
		if closure[name] {
			return
		}
		closure[name] = true
		svc, ok := m.cfg.Services[name]
		if !ok {
			return
		}
		for _, dependency := range svc.DependsOn {
			visit(dependency)
		}
	}
	for _, name := range names {
		visit(name)
	}
	return closure
}

// dependentClosure expands names with every transitive dependent, the set
// StopServices touches.
func (m *Model) dependentClosure(names []string) map[string]bool {
	closure := nameSet(names)
	graph := m.cfg.GetDependsOn()
	for changed := true; changed; {
		changed = false
		for name, dependencies := range graph {
			if closure[name] {
				continue
			}
			for _, dependency := range dependencies {
				if closure[dependency] {
					closure[name] = true
					changed = true
					break
				}
			}
		}
	}
	return closure
}

func (m *Model) restartAffectedServices(names []string) map[string]bool {
	dependents := m.dependentClosure(names)
	expanded := make([]string, 0, len(dependents))
	for name := range dependents {
		expanded = append(expanded, name)
	}
	return unionSets(m.dependencyClosure(expanded), dependents)
}

func (m *Model) allServiceNames() map[string]bool {
	return nameSet(m.cfg.ServiceNames())
}

func nameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func unionSets(left, right map[string]bool) map[string]bool {
	union := make(map[string]bool, len(left)+len(right))
	for name := range left {
		union[name] = true
	}
	for name := range right {
		union[name] = true
	}
	return union
}

func setsIntersect(left, right map[string]bool) bool {
	small, large := left, right
	if len(small) > len(large) {
		small, large = large, small
	}
	for name := range small {
		if large[name] {
			return true
		}
	}
	return false
}

func (m *Model) overlappingOperations(affected map[string]bool) []*activeOperation {
	var overlapping []*activeOperation
	for _, op := range m.operations {
		if setsIntersect(op.services, affected) {
			overlapping = append(overlapping, op)
		}
	}
	return overlapping
}

// canInterruptOperations preserves the dashboard's stop/force escape hatch
// without racing an operation that cannot actually be cancelled. Only starts
// blocked in a dependency or prerequisite wait carry cancellation handles.
func (m *Model) canInterruptOperations(kind operationKind, operations []*activeOperation) bool {
	switch kind {
	case operationStopSet, operationStopAll, operationForceStart, operationForceStop:
	default:
		return false
	}
	for _, op := range operations {
		if op.cancel == nil || (op.kind != operationStart && op.kind != operationStartSet) {
			return false
		}
	}
	return true
}

func newestOperation(operations []*activeOperation) *activeOperation {
	newest := operations[0]
	for _, op := range operations[1:] {
		if op.id > newest.id {
			newest = op
		}
	}
	return newest
}

func (m *Model) abortOperations(operations []*activeOperation) {
	for _, op := range operations {
		if op.cancel != nil {
			op.cancel()
		}
		delete(m.operations, op.id)
	}
	m.refreshOperationMirror()
}

func (m *Model) abortAllOperations() {
	for _, op := range m.operations {
		if op.cancel != nil {
			op.cancel()
		}
	}
	m.operations = make(map[int]*activeOperation)
	m.refreshOperationMirror()
}

// refreshOperationMirror keeps the single-valued display fields in sync with
// the newest tracked operation; the view and the legacy tests read them.
func (m *Model) refreshOperationMirror() {
	var newest *activeOperation
	for _, op := range m.operations {
		if newest == nil || op.id > newest.id {
			newest = op
		}
	}
	if newest == nil {
		m.operation = ""
		m.operationKind = ""
		m.operationCancel = nil
		return
	}
	m.operation = newest.label
	m.operationKind = newest.kind
	m.operationCancel = newest.cancel
}

func (m *Model) cancelStartOperation() {
	for id, op := range m.operations {
		if op.kind == operationStart || op.kind == operationStartSet {
			if op.cancel != nil {
				op.cancel()
			}
			delete(m.operations, id)
		}
	}
	m.refreshOperationMirror()
}

func (m *Model) handleOperationResult(msg operationResultMsg) (tea.Model, tea.Cmd) {
	if _, ok := m.operations[msg.id]; ok {
		delete(m.operations, msg.id)
		m.refreshOperationMirror()
	} else if msg.id != m.operationID {
		return m, nil
	}
	// A snapshot taken before this operation ran is now stale: refresh
	// immediately rather than waiting for the next 250ms tick, so a test or
	// a fast follow-up keypress sees the state the operation just produced.
	m.refreshServices()
	if msg.err != nil {
		var conflict *app.PortConflictError
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
	m.detachOnExit = false
	return m.beginExit("Shutting down")
}

func (m *Model) beginDetach() (tea.Model, tea.Cmd) {
	m.detachOnExit = true
	return m.beginExit("Detaching")
}

func (m *Model) beginCloseAndChoose() (tea.Model, tea.Cmd) {
	if !m.switcherSupported() || m.exiting {
		return m, nil
	}
	m.abortAllOperations()
	m.operationID++
	m.operationKind = ""
	m.operation = "Closing runtime"
	m.mode = ModeNormal
	m.exiting = true
	// Capture the departing application just as beginExit freezes m.app. The
	// chooser may install another session only after this command answers, and
	// that new session must never become the target of the close request.
	currentApp := m.app
	stopped := m.stopLogCommands()
	return m, func() tea.Msg {
		waitStoppedLogCommands(stopped)
		return shutdownResultMsg{err: currentApp.Shutdown(), chooseAfterClose: true}
	}
}

func (m *Model) beginExit(operation string) (tea.Model, tea.Cmd) {
	m.abortAllOperations()
	m.operationID++
	m.operationKind = ""
	if m.operation != operation {
		m.operation = operation
	}
	m.mode = ModeNormal
	// Freeze m.app from this point on: Shutdown() reads it on the goroutine
	// this command spawns, and a switch installing a new session afterward
	// would otherwise race that read and could shut down the wrong runtime.
	// Every switch entry point checks m.exiting before touching m.app.
	m.exiting = true
	stopped := m.stopLogCommands()
	return m, func() tea.Msg { waitStoppedLogCommands(stopped); return shutdownResultMsg{err: m.Shutdown()} }
}

func (m *Model) handleConfirmQuitKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.beginShutdown()
	case "d", "D":
		return m.beginDetach()
	case "c", "C":
		return m.beginCloseAndChoose()
	case "n", "N", "esc":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) handleConfirmRestartKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.confirmRestart()
	case "n", "N", "esc":
		m.confirmRestartAll = false
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) confirmRestart() (tea.Model, tea.Cmd) {
	if m.confirmRestartAll {
		m.confirmRestartAll = false
		m.mode = ModeNormal
		return m.beginOperation(operationRestartAll, "running services", "Restarting services", nil, m.app.RestartAll)
	}
	return m.beginRestart(m.confirmTarget)
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

func (m *Model) handleConfirmActionKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m, m.confirmPendingAction()
	case "n", "N", "esc":
		m.cancelPendingAction()
	}
	return m, nil
}
