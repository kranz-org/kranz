package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
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
			}
		}
	}
	groups := make([]string, 0, len(m.cfg.ActionGroups))
	for name := range m.cfg.ActionGroups {
		groups = append(groups, name)
	}
	sort.Strings(groups)
	for _, group := range groups {
		rows = append(rows, actionListRow{Kind: actionRowGroup, Group: group})
		if m.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, group)] {
			for _, id := range m.actionIDsFor(config.ActionOwnerGroup, group) {
				rows = append(rows, actionListRow{Kind: actionRowAction, Group: group, Action: id})
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
		if m.focusedAction != nil && row.Kind == actionRowAction && row.Action == *m.focusedAction {
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
	if m.focusedAction != nil {
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
	if state, ok := m.manager.ActionState(id); ok && state.Status == service.ActionRunning {
		if m.manager.CancelAction(id) {
			m.addNotification("action", "Stopping "+id.Name, config.LogWarn)
		} else {
			m.addNotification("action", id.Name+" is no longer running", config.LogWarn)
		}
		return nil, true
	}
	if action.ConfirmationRequired() {
		m.addNotification("action", id.Name+" requires confirmation", config.LogWarn)
		return nil, true
	}
	if action.InteractiveEnabled() {
		m.addNotification("action", id.Name+" requires terminal handoff", config.LogWarn)
		return nil, true
	}
	m.addNotification("action", "Running "+id.Name, config.LogInfo)
	return func() tea.Msg {
		result, err := m.manager.RunAction(context.Background(), id)
		return actionResultMsg{id: id, result: result, err: err}
	}, true
}

func (m *Model) handleActionResult(msg actionResultMsg) (tea.Model, tea.Cmd) {
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

func (m *Model) focusedActionDefinition() (config.ActionID, config.Action, service.ActionResult, bool) {
	if m.focusedAction == nil {
		return config.ActionID{}, config.Action{}, service.ActionResult{}, false
	}
	id := *m.focusedAction
	action, exists := m.cfg.ResolveAction(id)
	if !exists {
		return config.ActionID{}, config.Action{}, service.ActionResult{}, false
	}
	state, _ := m.manager.ActionState(id)
	return id, action, state, true
}
