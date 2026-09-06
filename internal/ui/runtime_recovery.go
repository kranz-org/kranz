package ui

import (
	"context"
	"errors"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

// Loss-of-current-runtime recovery (PRD 3.5): closing the current
// connection no longer quits the TUI. It moves the dashboard into
// ModeRuntimeLost, which offers restarting the same project or choosing
// another already-running one, without ever restarting automatically.

// runtimeRestartTimeout bounds how long "Restart runtime" waits for the new
// supervisor to publish itself to the registry and answer a handshake.
const runtimeRestartTimeout = 15 * time.Second

// restartPollInterval paces the registry polls "Restart runtime" makes while
// waiting for the new supervisor to publish its session.
const restartPollInterval = 200 * time.Millisecond

// currentRuntimeLostMsg reports that the current session's connection ended.
// sessionGen guards against a connection retired by a switch (whose Done()
// channel also closes) being mistaken for losing the runtime the dashboard
// is now showing.
type currentRuntimeLostMsg struct {
	sessionGen uint64
}

// watchCurrentClientDone waits on the current connection's Done() channel.
// It is restarted on every successful switch and every successful recovery,
// each time bound to the connection and generation active at that moment.
func (m *Model) watchCurrentClientDone() tea.Cmd {
	if m.rpcClient == nil {
		return nil
	}
	client := m.rpcClient
	sessionGen := m.sessionGeneration
	return func() tea.Msg {
		<-client.Done()
		return currentRuntimeLostMsg{sessionGen: sessionGen}
	}
}

func (m *Model) handleCurrentRuntimeLostMsg(msg currentRuntimeLostMsg) (tea.Model, tea.Cmd) {
	if msg.sessionGen != m.sessionGeneration || m.exiting || m.mode == ModeRuntimeLost {
		return m, nil
	}
	m.mode = ModeRuntimeLost
	m.recoveryReason = "Runtime stopped"
	m.recoveryErr = ""
	m.recoveryBusy = false
	m.recoveryShowingList = false
	return m, nil
}

// restartRuntimeMsg carries the outcome of "Restart runtime": either a fresh
// connection to the newly published supervisor, or why one never appeared.
type restartRuntimeMsg struct {
	sessionGen uint64
	seq        uint64
	record     kranzruntime.SessionRecord
	client     *kranzruntime.Client
	cfg        *config.Config
	err        error
}

func (m *Model) handleRuntimeLostKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.recoveryShowingList {
		switch {
		case key.Matches(msg, m.keys.Up):
			m.moveSwitcherCursor(-1)
		case key.Matches(msg, m.keys.Down):
			m.moveSwitcherCursor(1)
		case msg.String() == "enter":
			return m.connectToSwitcherSelection()
		case msg.String() == "esc":
			m.cancelPendingRuntimeSwitch()
			m.recoveryShowingList = false
		}
		return m, nil
	}
	switch msg.String() {
	case "r", "R", "enter":
		return m, m.beginRuntimeRestart()
	case "c", "C":
		if !m.switcherSupported() {
			return m, nil
		}
		// Choosing another runtime supersedes an in-progress reattach attempt.
		// The background process may already have started, but it must not pull
		// this TUI away from the chooser when its delayed result arrives.
		m.recoverySeq++
		m.recoveryBusy = false
		m.recoveryShowingList = true
		m.switcherGeneration++
		return m, m.refreshRuntimeList()
	case "q", "Q":
		m.recoverySeq++
		m.cancelPendingRuntimeSwitch()
		return m.beginDetach()
	}
	return m, nil
}

// beginRuntimeRestart spawns a new supervisor for the same project the lost
// session belonged to, using only its saved absolute directory and config
// paths — never a shell string built from user input (PRD 7) — then polls
// the registry until it publishes and answers a handshake.
func (m *Model) beginRuntimeRestart() tea.Cmd {
	if m.restartRuntime == nil || !m.switcherSupported() || m.recoveryBusy {
		return nil
	}
	m.recoveryBusy = true
	m.recoveryErr = ""
	m.recoverySeq++
	seq := m.recoverySeq
	sessionGen := m.sessionGeneration
	directory := m.sessionRecord.Directory
	configPaths := append([]string(nil), m.sessionConfigPaths...)
	runtimeName := m.sessionRecord.Name
	registry := m.registry
	clientVersion := m.version
	restart := m.restartRuntime
	return func() tea.Msg {
		if err := restart(directory, configPaths); err != nil {
			return restartRuntimeMsg{sessionGen: sessionGen, seq: seq, err: err}
		}
		deadline := time.Now().Add(runtimeRestartTimeout)
		for {
			resolveCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			record, resolveErr := registry.Resolve(resolveCtx, runtimeName, clientVersion)
			cancel()
			if resolveErr == nil {
				dialCtx, dialCancel := context.WithTimeout(context.Background(), time.Second)
				client, dialErr := kranzruntime.DialContextWithIdentity(dialCtx, record.Socket, clientVersion,
					kranzruntime.ClientIdentity{Surface: "tui", Label: "Kranz dashboard"})
				dialCancel()
				if dialErr == nil {
					cfg := client.Config()
					if cfg != nil {
						return restartRuntimeMsg{sessionGen: sessionGen, seq: seq, record: record, client: client, cfg: cfg}
					}
					_ = client.Close()
				}
			}
			if time.Now().After(deadline) {
				return restartRuntimeMsg{sessionGen: sessionGen, seq: seq, err: errors.New("the runtime did not come back up in time")}
			}
			time.Sleep(restartPollInterval)
		}
	}
}

func (m *Model) handleRestartRuntimeMsg(msg restartRuntimeMsg) (tea.Model, tea.Cmd) {
	if msg.sessionGen != m.sessionGeneration || msg.seq != m.recoverySeq {
		// Superseded: the user picked a different runtime from "Choose
		// running runtime" while this restart was still in flight.
		retireClient(msg.client)
		return m, nil
	}
	m.recoveryBusy = false
	if msg.err != nil {
		m.recoveryErr = msg.err.Error()
		return m, nil
	}
	// A restart of the same project is the one case PRD 3.4 allows carrying
	// UI state across a new session ID: seed the new session's cache slot
	// with what the lost session was showing, and let installSession's
	// ordinary by-name reconciliation decide what still applies.
	m.uiStateCache[msg.record.ID] = m.captureUIState()
	return m.installSession(msg.record, msg.client, msg.cfg)
}
