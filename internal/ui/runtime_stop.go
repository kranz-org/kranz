package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

type runtimeStopPlanMsg struct {
	seq   uint64
	plan  app.ShutdownPlan
	owned []string
	err   error
}

type runtimeStopResultMsg struct {
	seq    uint64
	record kranzruntime.SessionRecord
	err    error
}

func (m *Model) openRuntimeStop() (tea.Model, tea.Cmd) {
	if !m.switcherSupported() || m.switcherCursor < 0 || m.switcherCursor >= len(m.switcherRows) {
		return m, nil
	}
	row := m.switcherRows[m.switcherCursor]
	if row.IsCurrent {
		return m, nil
	}
	if row.Record.State != kranzruntime.SessionRunning && row.Record.State != kranzruntime.SessionIncompatible && row.Record.State != kranzruntime.SessionUnreachable {
		return m, nil
	}
	m.cancelPendingRuntimeSwitch()
	m.runtimeStopSeq++
	m.runtimeStopRecord = row.Record
	m.runtimeStopPlan = app.ShutdownPlan{}
	m.runtimeStopErr = ""
	m.runtimeStopBusy = false
	m.runtimeStopLoading = true
	m.runtimeStopOwned = nil
	m.mode = ModeRuntimeStop
	seq, record, version, registry := m.runtimeStopSeq, row.Record, m.version, m.registry
	return m, func() tea.Msg {
		if record.State == kranzruntime.SessionUnreachable {
			owned, err := registry.ForceDownPreview(record)
			return runtimeStopPlanMsg{seq: seq, owned: owned, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), runtimeSwitchTimeout)
		defer cancel()
		client, err := kranzruntime.DialContextForShutdown(ctx, record.Socket, version)
		if err != nil {
			return runtimeStopPlanMsg{seq: seq, err: err}
		}
		defer func() { _ = client.Close() }()
		plan, err := client.ShutdownPlanChecked()
		return runtimeStopPlanMsg{seq: seq, plan: plan, err: err}
	}
}

func (m *Model) handleRuntimeStopPlanMsg(msg runtimeStopPlanMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.runtimeStopSeq || m.mode != ModeRuntimeStop {
		return m, nil
	}
	m.runtimeStopLoading = false
	m.runtimeStopPlan = msg.plan
	m.runtimeStopOwned = msg.owned
	if msg.err != nil {
		m.runtimeStopErr = runtimeStopError("inspect", msg.err)
	}
	return m, nil
}

func (m *Model) handleRuntimeStopKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "s", "S", "y", "Y":
		return m.confirmRuntimeStop()
	case "esc", "n", "N":
		if !m.runtimeStopBusy {
			m.runtimeStopSeq++
			m.mode = ModeRuntimeSwitcher
			return m, m.refreshRuntimeList()
		}
	}
	return m, nil
}

func (m *Model) confirmRuntimeStop() (tea.Model, tea.Cmd) {
	if m.runtimeStopBusy || m.runtimeStopLoading || m.runtimeStopErr != "" {
		return m, nil
	}
	record := m.runtimeStopRecord
	m.runtimeStopBusy = true
	m.runtimeStopSeq++
	seq, registry, version, shownPlan := m.runtimeStopSeq, m.registry, m.version, m.runtimeStopPlan
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		fresh, err := registry.Resolve(ctx, record.ID, version)
		if err != nil {
			return runtimeStopResultMsg{seq: seq, record: record, err: err}
		}
		if fresh.ID != record.ID || fresh.State != record.State {
			return runtimeStopResultMsg{seq: seq, record: record, err: errors.New("runtime state changed; reopen the stop confirmation")}
		}
		if fresh.State == kranzruntime.SessionRunning || fresh.State == kranzruntime.SessionIncompatible {
			client, dialErr := kranzruntime.DialContextForShutdown(ctx, fresh.Socket, version)
			if dialErr != nil {
				return runtimeStopResultMsg{seq: seq, record: record, err: dialErr}
			}
			currentPlan, planErr := client.ShutdownPlanChecked()
			if planErr != nil {
				_ = client.Close()
				return runtimeStopResultMsg{seq: seq, record: record, err: planErr}
			}
			if !sameShutdownPlan(currentPlan, shownPlan) {
				_ = client.Close()
				return runtimeStopResultMsg{seq: seq, record: record, err: errors.New("shutdown plan changed; reopen the stop confirmation")}
			}
			err = client.Shutdown()
			_ = client.Close()
		} else {
			err = registry.ForceDown(ctx, fresh)
		}
		return runtimeStopResultMsg{seq: seq, record: record, err: err}
	}
}

func sameShutdownPlan(left, right app.ShutdownPlan) bool {
	return slices.Equal(left.Managed, right.Managed) &&
		slices.Equal(left.DetachedStop, right.DetachedStop) &&
		slices.Equal(left.DetachedKeep, right.DetachedKeep)
}

func (m *Model) handleRuntimeStopResultMsg(msg runtimeStopResultMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.runtimeStopSeq || m.mode != ModeRuntimeStop {
		return m, nil
	}
	m.runtimeStopBusy = false
	if msg.err != nil {
		m.runtimeStopErr = runtimeStopError("stop", msg.err)
		return m, nil
	}
	m.runtimeStopSeq++
	m.mode = ModeRuntimeSwitcher
	m.addNotification("runtime", "Stopped "+msg.record.Name, config.LogInfo)
	return m, m.refreshRuntimeList()
}

func runtimeStopError(operation string, err error) string {
	message := fmt.Sprintf("Could not %s runtime: %v", operation, err)
	var mismatch *kranzruntime.VersionMismatchError
	if errors.As(err, &mismatch) {
		return message + ". Stop it with the Kranz version that started the session."
	}
	var refused *kranzruntime.ForceDownError
	if errors.As(err, &refused) {
		return message + ". Forced shutdown needs verified process ownership; use the Kranz version that started the session to run `down`."
	}
	return message
}
