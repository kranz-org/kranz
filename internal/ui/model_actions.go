package ui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

func actionOwnerKey(kind config.ActionOwnerKind, owner string) string {
	return string(kind) + "\x00" + owner
}

func (m *Model) serviceListRows() []actionListRow {
	rows := make([]actionListRow, 0, len(m.services)+len(m.cfg.ActionGroups))
	for _, svc := range m.services {
		rows = append(rows, actionListRow{Kind: actionRowService, Service: svc})
		if m.expandedActionOwner[actionOwnerKey(config.ActionOwnerService, svc.Name)] {
			for _, id := range m.actionIDsFor(config.ActionOwnerService, svc.Name) {
				rows = append(rows, actionListRow{Kind: actionRowAction, Service: svc, Action: id})
				rows = append(rows, m.actionParamRows(id)...)
			}
		}
	}
	for _, group := range m.cfg.ActionGroupNames() {
		rows = append(rows, actionListRow{Kind: actionRowGroup, Group: group})
		if m.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, group)] {
			for _, id := range m.actionIDsFor(config.ActionOwnerGroup, group) {
				rows = append(rows, actionListRow{Kind: actionRowAction, Group: group, Action: id})
				rows = append(rows, m.actionParamRows(id)...)
			}
		}
	}
	return rows
}

func (m *Model) actionIDsFor(kind config.ActionOwnerKind, owner string) []config.ActionID {
	ids := make([]config.ActionID, 0)
	for _, id := range m.cfg.ActionIDs() {
		if id.OwnerKind == kind && id.Owner == owner {
			ids = append(ids, id)
		}
	}
	return ids
}

func (m *Model) focusedServiceListRow() int {
	rows := m.serviceListRows()
	for index, row := range rows {
		// A parameter focus names exactly one row. Matching a value focus
		// loosely would return the parameter's own row, which sits above its
		// values, and the cursor could never move past the first of them.
		if focus := m.focusedParam; focus != nil {
			if row.Action != focus.ID {
				continue
			}
			switch {
			case focus.Preview:
				if row.Kind == actionRowPreview {
					return index
				}
			case focus.IsValue:
				if row.Kind == actionRowParamValue && row.Param == focus.Name && row.Value == focus.Value {
					return index
				}
			default:
				if row.Kind == actionRowParam && row.Param == focus.Name {
					return index
				}
			}
			continue
		}
		if m.focusedParam == nil && m.focusedAction != nil && row.Kind == actionRowAction && row.Action == *m.focusedAction {
			return index
		}
		if m.focusedAction == nil && m.focusedActionGroup != "" && row.Kind == actionRowGroup && row.Group == m.focusedActionGroup {
			return index
		}
		if m.focusedAction == nil && m.focusedActionGroup == "" && row.Kind == actionRowService {
			svc := m.FocusedService()
			if svc != nil && row.Service == svc {
				return index
			}
		}
	}
	if len(rows) > 0 {
		return 0
	}
	return -1
}

func (m *Model) focusServiceListRow(index int) {
	rows := m.serviceListRows()
	if index < 0 || index >= len(rows) {
		return
	}
	row := rows[index]
	m.focusedAction = nil
	m.focusedActionGroup = ""
	m.focusedParam = nil
	switch row.Kind {
	case actionRowService:
		m.focusServiceByName(row.Service.Name)
		m.resetActionView()
	case actionRowGroup:
		m.focusedActionGroup = row.Group
		m.resetActionView()
	case actionRowAction:
		if row.Service != nil {
			m.focusServiceByName(row.Service.Name)
		}
		id := row.Action
		m.focusedAction = &id
		m.resetActionView()
	case actionRowParam:
		if row.Service != nil {
			m.focusServiceByName(row.Service.Name)
		} else if row.Group != "" {
			m.focusedActionGroup = row.Group
		}
		id := row.Action
		m.focusedAction = &id
		m.focusedParam = &paramRowFocus{ID: id, Name: row.Param}
		m.resetActionView()
	case actionRowParamValue:
		if row.Service != nil {
			m.focusServiceByName(row.Service.Name)
		} else if row.Group != "" {
			m.focusedActionGroup = row.Group
		}
		id := row.Action
		m.focusedAction = &id
		m.focusedParam = &paramRowFocus{ID: id, Name: row.Param, Value: row.Value, IsValue: true}
		m.resetActionView()
	case actionRowPreview:
		id := row.Action
		m.focusedAction = &id
		m.focusedParam = &paramRowFocus{ID: id, Preview: true}
	}
}

func (m *Model) focusServiceByName(name string) {
	for index, svc := range m.services {
		if svc.Name == name {
			if index != m.focused {
				m.moveFocus(index)
			}
			return
		}
	}
}

func (m *Model) resetActionView() {
	m.detailOffset = 0
	m.logOffset = 0
	m.logAnchor = 0
	m.followMode = true
	m.logPaused = false
}

func (m *Model) moveServiceListCursor(direction int) {
	rows := m.serviceListRows()
	if len(rows) == 0 {
		return
	}
	current := m.focusedServiceListRow()
	if current < 0 {
		current = 0
	}
	next := min(len(rows)-1, max(0, current+direction))
	// A command continued over several rows is one stop, not several: its
	// continuation rows carry no focus of their own, and landing on one would
	// leave the cursor unable to move on.
	for next > 0 && next < len(rows)-1 && rows[next].Continued {
		next += direction
	}
	for next > 0 && rows[next].Continued {
		next--
	}
	if next != current {
		m.focusServiceListRow(next)
	}
}

func (m *Model) toggleFocusedActionOwner() bool {
	if m.focusedAction != nil {
		return false
	}
	kind := config.ActionOwnerService
	owner := ""
	if m.focusedActionGroup != "" {
		kind = config.ActionOwnerGroup
		owner = m.focusedActionGroup
	} else if svc := m.FocusedService(); svc != nil {
		owner = svc.Name
	}
	if owner == "" || len(m.actionIDsFor(kind, owner)) == 0 {
		return false
	}
	key := actionOwnerKey(kind, owner)
	m.expandedActionOwner[key] = !m.expandedActionOwner[key]
	m.detailOffset = 0
	return true
}

func (m *Model) openFocusedListItem() (tea.Cmd, bool) {
	if m.listMode != listServices {
		return nil, false
	}
	if m.focusedParam != nil {
		return m.toggleFocusedParamRow()
	}
	if m.focusedAction != nil {
		id := *m.focusedAction
		if m.actionHasParams(id) {
			m.expandedActionParams[id] = !m.expandedActionParams[id]
			m.focusedParam = nil
			return nil, true
		}
		return nil, true
	}
	return nil, m.toggleFocusedActionOwner()
}

func (m *Model) toggleFocusedAction() (tea.Cmd, bool) {
	if m.listMode != listServices || m.focusedAction == nil {
		return nil, false
	}
	id := *m.focusedAction
	action, exists := m.cfg.ResolveAction(id)
	if !exists {
		m.addNotification("action", "Action is no longer configured", config.LogWarn)
		return nil, true
	}
	if state, ok := m.app.ActionState(id); ok && state.Status == app.ActionRunning {
		m.beginActionConfirmation(id, true)
		return nil, true
	}
	if m.actionHasParams(id) {
		return m.runParameterizedAction(id), true
	}
	// An interactive action always confirms, whether or not it asked to. Taking
	// over the terminal removes Kranz from the screen, and that must never
	// happen to someone who only pressed a key in a list.
	if action.ConfirmationRequired() || action.InteractiveEnabled() {
		m.beginActionConfirmation(id, false)
		return nil, true
	}
	return m.runAction(id, action), true
}

// runInteractiveAction hands the terminal to an action that has to be answered,
// such as a migration that asks before it writes. Kranz suspends its interface,
// the command owns the terminal until it exits, and the outcome is recorded
// like any other action.
func (m *Model) runInteractiveAction(id config.ActionID) tea.Cmd {
	action, lease, err := m.app.AcquireInteractiveAction(id)
	if err != nil {
		m.addNotification("action", id.Name+": "+err.Error(), config.LogError)
		return nil
	}
	return m.runAcquiredInteractiveAction(id, action, lease)
}

func (m *Model) runAcquiredInteractiveAction(id config.ActionID, action config.Action, lease string) tea.Cmd {
	application, sessionGen := m.app, m.sessionGeneration
	command := app.BuildInteractiveCommand(action)
	m.addNotification("action", "Handing the terminal to "+id.Name, config.LogInfo)
	return m.execTerminalProcess(command, func(execErr error) tea.Msg {
		// The application layer never ran this command — it may not even
		// live in this process — so this caller is the only one that can
		// read the exit code and PID it observed.
		exitCode, pid := 0, 0
		if command.ProcessState != nil {
			exitCode = command.ProcessState.ExitCode()
			if command.Process != nil {
				pid = command.Process.Pid
			}
		}
		result, completeErr := application.CompleteInteractiveAction(id, lease, execErr, exitCode, pid)
		if execErr == nil {
			execErr = completeErr
		}
		return actionResultMsg{id: id, result: result, err: execErr, sessionGen: sessionGen}
	})
}

func (m *Model) beginActionConfirmation(id config.ActionID, stop bool) {
	pending := id
	m.pendingAction = &pending
	m.pendingActionStop = stop
	m.mode = ModeConfirmAction
}

func (m *Model) runAction(id config.ActionID, action config.Action) tea.Cmd {
	if action.InteractiveEnabled() {
		return m.runInteractiveAction(id)
	}
	m.addNotification("action", "Running "+id.Name, config.LogInfo)
	application, sessionGen := m.app, m.sessionGeneration
	release := retainRuntimeApplication(application)
	return func() tea.Msg {
		defer release()
		result, err := application.RunAction(context.Background(), id)
		return actionResultMsg{id: id, result: result, err: err, sessionGen: sessionGen}
	}
}

func (m *Model) confirmPendingAction() tea.Cmd {
	if m.pendingParamRequest != nil {
		request := *m.pendingParamRequest
		token := m.pendingParamToken
		m.pendingParamRequest = nil
		m.pendingParamToken = ""
		m.pendingAction = nil
		m.pendingActionStop = false
		m.mode = ModeNormal
		return m.executeParamPlan(request, token)
	}
	id := m.pendingAction
	stop := m.pendingActionStop
	m.pendingAction = nil
	m.pendingActionStop = false
	m.mode = ModeNormal
	if id == nil {
		return nil
	}
	if stop {
		if m.app.CancelAction(*id) {
			m.addNotification("action", "Stopping "+id.Name, config.LogWarn)
		} else {
			m.addNotification("action", id.Name+" is no longer running", config.LogWarn)
		}
		return nil
	}
	action, exists := m.cfg.ResolveAction(*id)
	if !exists {
		m.addNotification("action", "Action is no longer configured", config.LogWarn)
		return nil
	}
	return m.runAction(*id, action)
}

func (m *Model) cancelPendingAction() {
	m.pendingAction = nil
	m.pendingActionStop = false
	m.pendingParamRequest = nil
	m.pendingParamToken = ""
	m.mode = ModeNormal
}

func (m *Model) handleActionResult(msg actionResultMsg) (tea.Model, tea.Cmd) {
	m.actionStates[msg.id] = msg.result
	m.refreshRunSummaries()
	m.refreshLogCache(app.ActionRunTarget(msg.id))
	level := config.LogInfo
	message := fmt.Sprintf("%s %s in %s", msg.id.Name, msg.result.Status.String(), msg.result.Duration.Round(10*time.Millisecond))
	if msg.err != nil {
		level = config.LogError
		if errors.Is(msg.err, context.Canceled) {
			level = config.LogWarn
		}
		if msg.result.StartedAt.IsZero() {
			message = msg.id.Name + ": " + msg.err.Error()
		} else {
			message += ": " + msg.err.Error()
		}
	}
	m.addNotification("action", message, level)
	return m, nil
}

func (m *Model) focusedActionDefinition() (config.ActionID, config.Action, app.ActionResult, bool) {
	if m.focusedAction == nil {
		return config.ActionID{}, config.Action{}, app.ActionResult{}, false
	}
	id := *m.focusedAction
	action, exists := m.cfg.ResolveAction(id)
	if !exists {
		return config.ActionID{}, config.Action{}, app.ActionResult{}, false
	}
	state := m.cachedActionState(id)
	return id, action, state, true
}
