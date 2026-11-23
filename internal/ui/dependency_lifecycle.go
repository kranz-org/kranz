package ui

import (
	"context"
	"fmt"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) runDependencyOperation() (tea.Model, tea.Cmd) {
	state := m.dependencyModal
	if state.runID != 0 || state.runMessage != "" {
		return m, nil
	}
	preview, err := m.previewDependencyOperation(state.operation, state.selectedOnly)
	if !preview.runnable(err) {
		return m, nil
	}

	names := slices.Clone(preview.Requested)
	if state.operation == "restart" {
		names = slices.Clone(preview.restartTargets)
	}
	target := names[0]
	if len(names) > 1 {
		target = fmt.Sprintf("%d selected services", len(names))
	}
	application := m.app
	var cmd tea.Cmd
	switch {
	case state.selectedOnly && state.operation == "start":
		names = m.orderSelectedStarts(names)
		_, cmd = m.beginOperation(operationForceStart, target, "Starting "+target, names, func() error {
			return application.ForceStartServices(names)
		})
	case state.operation == "start":
		ctx, cancel := context.WithCancel(context.Background())
		_, cmd = m.beginCancelableOperation(operationStartSet, target, "Starting "+target, names, cancel, func() error {
			return application.StartServicesContext(ctx, names)
		})
	case state.operation == "stop":
		kind, stop := operationStopSet, application.StopServices
		if state.selectedOnly {
			kind, stop = operationForceStop, application.ForceStopServices
		}
		_, cmd = m.beginOperation(kind, target, "Stopping "+target, names, func() error {
			return stop(names)
		})
	case state.operation == "restart":
		kind, restart := operationRestartSet, application.RestartServices
		if state.selectedOnly {
			kind, restart = operationForceRestart, application.ForceRestartServices
		}
		_, cmd = m.beginOperation(kind, target, "Restarting "+target, names, func() error {
			return restart(names)
		})
	}
	m.dependencyModal.confirming = false
	if cmd != nil {
		m.dependencyModal.runID = m.operationID
	} else {
		m.dependencyModal.runFailed = true
		m.dependencyModal.runMessage = "Wait for the current operation"
		m.dependencyModal.planOffset = 0
	}
	return m, cmd
}

func (m *Model) finishDependencyRun(msg operationResultMsg) bool {
	state := &m.dependencyModal
	if state.runID == 0 || msg.id != state.runID {
		return false
	}
	state.runID = 0
	label := dependencyOperationLabels[state.operation]
	state.runFailed = msg.err != nil
	if state.runFailed {
		state.runMessage = label + " failed — " + msg.err.Error()
		state.planOffset = 0
		return true
	}
	state.runMessage = label + " complete"
	return true
}
