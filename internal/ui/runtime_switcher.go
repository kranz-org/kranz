package ui

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

// The runtime switcher: a modal list of every locally registered runtime,
// reached with `p` from the dashboard, that lets the TUI attach to another
// project without restarting the process (PRD 3.2/3.3).

// switcherRefreshInterval bounds how often an open switcher re-probes the
// registry. Faster than this wastes sockets on a list that mostly repeats
// itself; slower would make a runtime that just started or stopped feel
// unresponsive in the modal (PRD: "actualization ~once a second").
const switcherRefreshInterval = time.Second

// runtimeSwitchTimeout bounds dialing and handshaking a switch target. It is
// generous relative to a local Unix-socket connection so a momentarily busy
// supervisor is not mistaken for an unreachable one.
const runtimeSwitchTimeout = 5 * time.Second

// switchTargetMsg carries the outcome of dialing and handshaking a switch
// target. seq guards against a superseded attempt (the user picked another
// target, or closed the switcher, before this one answered) mutating the
// dashboard.
type switchTargetMsg struct {
	seq    uint64
	record kranzruntime.SessionRecord
	client *kranzruntime.Client
	cfg    *config.Config
	err    error
}

// switcherSupported reports whether this model is attached to a real local
// registry and can therefore switch or recover at all. It is false for
// tests and embedders that supply their own app.API directly, which makes
// every switcher and recovery entry point a no-op rather than a nil risk —
// exactly the tests and embedding contracts that predate this feature.
func (m *Model) switcherSupported() bool {
	return m.registry != nil
}

func (m *Model) openRuntimeSwitcher() tea.Cmd {
	if !m.switcherSupported() || m.exiting {
		return nil
	}
	m.mode = ModeRuntimeSwitcher
	m.switcherLoading = true
	m.switcherErr = ""
	m.switcherGeneration++
	return m.refreshRuntimeList()
}

func (m *Model) closeRuntimeSwitcher() {
	m.cancelPendingRuntimeSwitch()
	m.mode = ModeNormal
}

func (m *Model) cancelPendingRuntimeSwitch() {
	if m.switcherConnecting == "" {
		return
	}
	m.switchSeq++
	m.switcherConnecting = ""
}

// refreshRuntimeList dispatches one discovery round, single-flighted so an
// auto-refresh tick never stacks a second probe on top of one still running
// (PRD 8: "no accumulating parallel refresh cycles").
func (m *Model) refreshRuntimeList() tea.Cmd {
	if !m.switcherSupported() || m.switcherRefreshBusy {
		return nil
	}
	m.switcherRefreshBusy = true
	m.lastSwitcherRefresh = time.Now()
	generation := m.switcherGeneration
	registry := m.registry
	clientVersion := m.version
	sessionID := m.sessionID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeDiscoveryTimeout)
		defer cancel()
		rows, err := discoverRuntimeRows(ctx, registry, clientVersion, sessionID)
		return runtimeListMsg{generation: generation, rows: rows, err: err}
	}
}

// refreshRuntimeListIfVisible is folded into the dashboard's regular tick so
// an open switcher (or the "choose running runtime" list inside recovery)
// keeps updating without the user ever needing to reopen it.
func (m *Model) refreshRuntimeListIfVisible() tea.Cmd {
	if m.mode != ModeRuntimeSwitcher && (m.mode != ModeRuntimeLost || !m.recoveryShowingList) {
		return nil
	}
	if !m.lastSwitcherRefresh.IsZero() && time.Since(m.lastSwitcherRefresh) < switcherRefreshInterval {
		return nil
	}
	return m.refreshRuntimeList()
}

func (m *Model) handleRuntimeListMsg(msg runtimeListMsg) (tea.Model, tea.Cmd) {
	m.switcherRefreshBusy = false
	if msg.generation != m.switcherGeneration {
		if m.mode == ModeRuntimeSwitcher || (m.mode == ModeRuntimeLost && m.recoveryShowingList) {
			return m, m.refreshRuntimeList()
		}
		return m, nil
	}
	m.switcherLoading = false
	if msg.err != nil {
		m.switcherErr = msg.err.Error()
		return m, nil
	}
	m.switcherErr = ""
	previousID := ""
	if m.switcherCursor >= 0 && m.switcherCursor < len(m.switcherRows) {
		previousID = m.switcherRows[m.switcherCursor].Record.ID
	}
	m.switcherRows = msg.rows
	newIndex := -1
	for index, row := range m.switcherRows {
		if row.Record.ID == previousID {
			newIndex = index
			break
		}
	}
	if newIndex < 0 {
		// The row the cursor was on disappeared: land on the nearest
		// remaining row instead of resetting to the top (PRD 3.2).
		newIndex = min(m.switcherCursor, max(0, len(m.switcherRows)-1))
	}
	m.switcherCursor = newIndex
	return m, nil
}

func (m *Model) handleCloseAndChooseResult(closeErr error) (tea.Model, tea.Cmd) {
	m.exiting = false
	m.detachOnExit = true
	m.operation = ""
	m.sessionGeneration++
	m.mode = ModeRuntimeLost
	m.recoveryReason = "Runtime closed"
	m.recoveryBusy = false
	m.recoveryShowingList = true
	m.recoveryErr = ""
	if closeErr != nil {
		m.recoveryErr = "Could not close current runtime cleanly: " + closeErr.Error()
	}
	m.switcherRows = nil
	m.switcherCursor = 0
	m.switcherLoading = true
	m.switcherErr = ""
	m.switcherGeneration++
	return m, m.refreshRuntimeList()
}

func (m *Model) handleRuntimeSwitcherKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.moveSwitcherCursor(-1)
	case key.Matches(msg, m.keys.Down):
		m.moveSwitcherCursor(1)
	case msg.String() == "enter":
		return m.connectToSwitcherSelection()
	case msg.String() == "esc", msg.String() == "p":
		m.closeRuntimeSwitcher()
	}
	return m, nil
}

func (m *Model) moveSwitcherCursor(delta int) {
	if len(m.switcherRows) == 0 {
		return
	}
	m.switcherCursor = min(max(0, m.switcherCursor+delta), len(m.switcherRows)-1)
}

func (m *Model) connectToSwitcherSelection() (tea.Model, tea.Cmd) {
	if m.switcherCursor < 0 || m.switcherCursor >= len(m.switcherRows) {
		return m, nil
	}
	row := m.switcherRows[m.switcherCursor]
	if !row.Selectable || row.IsCurrent {
		return m, nil
	}
	return m, m.beginSwitchTo(row.Record)
}

// beginSwitchTo dials and handshakes record without touching m.app, m.cfg,
// or any dashboard state yet: the switch only becomes visible once
// handleSwitchTargetMsg confirms the connection, per the PRD 3.3 algorithm
// ("dashboard changes current runtime only after successful connection").
func (m *Model) beginSwitchTo(record kranzruntime.SessionRecord) tea.Cmd {
	if !m.switcherSupported() || m.exiting || m.switcherConnecting != "" {
		return nil
	}
	m.switcherConnecting = record.ID
	m.switchSeq++
	seq := m.switchSeq
	clientVersion := m.version
	socket := record.Socket
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeSwitchTimeout)
		defer cancel()
		client, err := kranzruntime.DialContextWithIdentity(ctx, socket, clientVersion,
			kranzruntime.ClientIdentity{Surface: "tui", Label: "Kranz dashboard"})
		if err != nil {
			return switchTargetMsg{seq: seq, record: record, err: err}
		}
		cfg := client.Config()
		if cfg == nil {
			_ = client.Close()
			return switchTargetMsg{seq: seq, record: record, err: errors.New("runtime returned no effective configuration")}
		}
		return switchTargetMsg{seq: seq, record: record, client: client, cfg: cfg}
	}
}

func (m *Model) handleSwitchTargetMsg(msg switchTargetMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.switchSeq {
		// A newer attempt (or leaving the switcher and coming back)
		// superseded this one. The connection was never installed anywhere,
		// so nothing but its own socket depends on it.
		retireClient(msg.client)
		return m, nil
	}
	m.switcherConnecting = ""
	if m.exiting {
		retireClient(msg.client)
		return m, nil
	}
	if msg.err != nil {
		m.switcherErr = "Could not connect: " + msg.err.Error()
		return m, m.refreshRuntimeList()
	}
	return m.installSession(msg.record, msg.client, msg.cfg)
}

// installSession is the atomic "commit" step of a switch: it saves the
// departing session's UI state, points the model at the new connection, and
// restores (or defaults) the arriving session's UI state, all synchronously
// within one Update call so no render or async result can observe a mixed
// state (PRD 3.3's "atomically shows the selected project").
func (m *Model) installSession(record kranzruntime.SessionRecord, client *kranzruntime.Client, cfg *config.Config) (tea.Model, tea.Cmd) {
	if m.sessionID != "" {
		m.uiStateCache[m.sessionID] = m.captureUIState()
	}
	arrivingState := m.uiStateCache[record.ID]
	oldClient := m.rpcClient
	project := client.Project()
	m.app = client
	m.rpcClient = client
	m.cfg = cfg
	m.sessionID = record.ID
	m.sessionRecord = record
	m.sessionConfigPaths = append([]string(nil), project.ConfigPaths...)
	if len(m.sessionConfigPaths) == 0 {
		m.sessionConfigPaths = append([]string(nil), cfg.Paths...)
	}
	m.configPaths = append([]string(nil), m.sessionConfigPaths...)
	m.workingDirectory = record.Directory
	if m.workingDirectory == "" && len(m.configPaths) > 0 {
		m.workingDirectory = filepath.Dir(m.configPaths[0])
	}
	m.sessionGeneration++
	m.mode = ModeNormal
	m.recoveryShowingList = false
	m.recoveryReason = ""
	m.recoveryErr = ""
	m.projectExitHandled = false
	m.configGeneration = project.Generation
	m.lastConfigCheck = time.Time{}
	m.cancelPendingRuntimeSwitch()
	m.recoverySeq++

	// An operation started in the previous session belongs to that supervisor.
	// Leave its context and connection alone so it can finish there, but do not
	// let its spinner or cancellation handle block commands in the new runtime.
	m.operationID++
	m.operation = ""
	m.operationKind = ""
	m.operationCancel = nil

	m.resetRuntimeDataCaches()
	m.allServices = m.app.Services()
	m.services = m.allServices
	m.refreshRunSummaries()
	m.refreshActionStates()
	m.applyUIState(arrivingState)
	delete(m.uiStateCache, m.sessionID)
	m.refreshVisibleLogCaches()
	rows := m.tagRows()
	m.tagCursor = min(max(0, len(rows)-1), max(0, m.tagCursor))
	if len(m.services) == 0 {
		m.focused = 0
	} else if m.focused >= len(m.services) {
		m.focused = len(m.services) - 1
	}
	m.markFocusedRead()
	m.portService, m.portChecked, m.portScanBusy = "", time.Time{}, false
	m.portScanID++

	if err := m.applyEffectiveAppearance(); err != nil {
		m.activeTheme, _ = applyAppearance(DefaultTheme, "", backgroundTerminal, colorModeAuto, m.terminalDark)
		m.addNotification("appearance", err.Error()+"; using the Kranz theme", config.LogWarn)
	}
	for _, diagnostic := range cfg.Diagnostics {
		m.addNotification("config", diagnostic, config.LogWarn)
	}

	if oldClient != nil {
		retireClient(oldClient)
	}

	m.addNotification("runtime", "Switched to "+record.Name, config.LogInfo)
	return m, tea.Batch(m.pollServices(), m.scanFocusedPorts(true), m.watchCurrentClientDone())
}

// CloseCurrentConnection closes whichever connection this model is
// currently attached to over the wire, if any. It only hangs up this
// client's own socket — asking the runtime to stop anything is Shutdown()'s
// job — so it is always safe to call once the TUI program has stopped, and
// is the only way for a caller to close the right connection after a
// session that switched runtimes: the caller's own original client may
// already have been retired.
func (m *Model) CloseCurrentConnection() error {
	if m.rpcClient == nil {
		return nil
	}
	return m.rpcClient.Close()
}

// retireClient closes a connection this model no longer references, once
// any request already accepted by its supervisor has answered, without
// blocking the caller (PRD 3.3, risk table: retiring a client must never
// cancel operation the server already committed to).
func retireClient(client *kranzruntime.Client) {
	if client == nil {
		return
	}
	// There is deliberately no timeout here. A switch is a detach, and the
	// product contract says work already accepted by the old supervisor keeps
	// running even when readiness or an action takes longer than expected.
	client.CloseAfterIdle(0)
}

func retainRuntimeApplication(application app.API) func() {
	if client, ok := application.(*kranzruntime.Client); ok {
		return client.Retain()
	}
	return func() {}
}
