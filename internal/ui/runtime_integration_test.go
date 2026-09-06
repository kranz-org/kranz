package ui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

// testRuntime is one real local supervisor, registered in a real (temp)
// registry and reachable over a real Unix socket — the same stack a live
// `kranz up -d` builds — so the switching tests below exercise the actual
// discovery, handshake, and RPC path rather than a fake app.API.
type testRuntime struct {
	name       string
	local      *app.Local
	supervisor *kranzruntime.Supervisor
	session    *kranzruntime.SessionHandle
	client     *kranzruntime.Client
	record     kranzruntime.SessionRecord
	serveErr   chan error
	closed     bool
}

func startTestRuntime(t *testing.T, registry *kranzruntime.Registry, name string, cfg *config.Config, directory string) *testRuntime {
	t.Helper()
	session, err := registry.Acquire(name)
	if err != nil {
		t.Fatalf("acquire %s: %v", name, err)
	}
	metadata, err := session.Prepare(cfg.Project, "test", "background", directory)
	if err != nil {
		t.Fatalf("prepare %s: %v", name, err)
	}
	local := app.NewLocal(cfg, []string{directory + "/kranz.yaml"}, app.Options{SessionID: metadata.ID})
	supervisor := kranzruntime.NewSupervisor(local)
	if err := supervisor.Listen(metadata.Socket); err != nil {
		t.Fatalf("listen %s: %v", name, err)
	}
	if err := session.Publish(); err != nil {
		t.Fatalf("publish %s: %v", name, err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- supervisor.Serve() }()

	var client *kranzruntime.Client
	deadline := time.Now().Add(2 * time.Second)
	for {
		client, err = kranzruntime.DialWithIdentity(metadata.Socket, "test",
			kranzruntime.ClientIdentity{Surface: "tui", Label: "test-" + name})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial %s: %v", name, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	record, err := registry.Resolve(context.Background(), name, "test")
	if err != nil {
		t.Fatalf("resolve %s: %v", name, err)
	}

	rt := &testRuntime{name: name, local: local, supervisor: supervisor, session: session, client: client, record: record, serveErr: serveErr}
	t.Cleanup(rt.stop)
	return rt
}

// stop tears the runtime down as if its process had exited: connections
// severed and its registry entry released. Idempotent, so it is safe both
// to call explicitly (to simulate a crash mid-test) and again from cleanup.
func (rt *testRuntime) stop() {
	if rt.closed {
		return
	}
	rt.closed = true
	_ = rt.client.Close()
	_ = rt.supervisor.Close()
	<-rt.serveErr
	_ = rt.session.Close()
	_ = rt.local.Shutdown()
}

func testRegistry(t *testing.T) *kranzruntime.Registry {
	t.Helper()
	registry, err := kranzruntime.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return registry
}

// fireOnce invokes cmd exactly once and, if it produced a message, feeds it
// through Update. It intentionally does not chase batched or long-blocking
// follow-up commands (a poll tick, watchCurrentClientDone's wait on Done())
// — tests drive exactly the message under test and assert on its effect.
func fireOnce(model *Model, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	_, next := model.Update(msg)
	return next
}

func newSwitchableTestModel(t *testing.T, registry *kranzruntime.Registry, current *testRuntime, options ModelOptions) *Model {
	t.Helper()
	options.App = current.client
	options.Registry = registry
	options.SessionRecord = current.record
	model := NewModelWithOptions(current.client.Config(), "test", options)
	t.Cleanup(func() { _ = model.Shutdown() })
	return model
}

func emptyProjectConfig(project string) *config.Config {
	return &config.Config{Project: project, Services: map[string]config.Service{}}
}

// serviceProjectConfig gives a test runtime one service, which is what the
// run-history half of the UI state needs: without a service there is no run
// target to select a run within.
func serviceProjectConfig(project, service string) *config.Config {
	return &config.Config{Project: project, Services: map[string]config.Service{
		service: {Command: "sleep 60", Dir: ".", Shell: "sh"},
	}}
}

// switchTo drives the full PRD 3.3 algorithm (discover, select, dial,
// handshake, install) against a real target and fails the test if the
// target never appears in discovery or the connection is refused.
func switchTo(t *testing.T, model *Model, target kranzruntime.SessionRecord) {
	t.Helper()
	next := fireOnce(model, model.openRuntimeSwitcher())
	_ = next
	found := false
	for index, row := range model.switcherRows {
		if row.Record.ID == target.ID {
			model.switcherCursor = index
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("target %s not discovered; rows=%#v (switcherErr=%q)", target.Name, model.switcherRows, model.switcherErr)
	}
	_, cmd := model.connectToSwitcherSelection()
	if cmd == nil {
		t.Fatal("connectToSwitcherSelection produced no command")
	}
	fireOnce(model, cmd)
	if model.switcherErr != "" {
		t.Fatalf("switch to %s failed: %s", target.Name, model.switcherErr)
	}
}

func TestRuntimeSwitcherEndToEndSwitchesAndRestoresState(t *testing.T) {
	registry := testRegistry(t)
	alpha := startTestRuntime(t, registry, "switch-alpha", serviceProjectConfig("Alpha", "api"), t.TempDir())
	beta := startTestRuntime(t, registry, "switch-beta", emptyProjectConfig("Beta"), t.TempDir())

	model := newSwitchableTestModel(t, registry, alpha, ModelOptions{})
	if err := alpha.client.StartServicesContext(context.Background(), []string{"api"}); err != nil {
		t.Fatalf("start api: %v", err)
	}
	model.refreshRunSummaries()
	if model.cfg.Project != "Alpha" {
		t.Fatalf("initial project = %q, want Alpha", model.cfg.Project)
	}
	firstGeneration := model.sessionGeneration

	// Mark Alpha's UI state distinctly so a bleed into Beta, or a failure to
	// restore it on return, is unambiguous.
	model.panelFocus = panelDetails
	model.wrapLogs = true
	// The run half of PRD 3.4's field list: which run is open, and where its
	// output is scrolled. Both are derived state elsewhere in the model, so
	// they are the parts a restore is most likely to quietly drop.
	model.syncRunTarget()
	model.runMode, model.selectedRun, model.runFollowsLatest = runViewSingle, 1, false
	alphaViewport := model.runViewportKey()
	model.runViewports[alphaViewport] = runViewportState{Offset: 17, Anchor: 4}
	model.operation = "Starting alpha"
	oldOperationCanceled := false
	model.operationCancel = func() { oldOperationCanceled = true }
	model.logEntries[app.ServiceRunTarget("shared-name")] = []config.LogEntry{{Text: "alpha-only"}}

	switchTo(t, model, beta.record)
	if model.cfg.Project != "Beta" {
		t.Fatalf("after switch, project = %q, want Beta", model.cfg.Project)
	}
	if model.sessionID != beta.record.ID {
		t.Fatalf("sessionID = %q, want %q", model.sessionID, beta.record.ID)
	}
	if model.sessionGeneration == firstGeneration {
		t.Fatal("sessionGeneration did not advance across a switch")
	}
	// Beta was never visited before: it gets Kranz's ordinary defaults, not
	// whatever Alpha happened to be showing.
	if model.panelFocus != panelServices || model.wrapLogs {
		t.Fatalf("beta inherited alpha's UI state: panelFocus=%v wrapLogs=%v", model.panelFocus, model.wrapLogs)
	}
	if model.workingDirectory != beta.record.Directory {
		t.Fatalf("working directory = %q, want target runtime directory %q", model.workingDirectory, beta.record.Directory)
	}
	if len(model.configPaths) != 1 || model.configPaths[0] != beta.record.Directory+"/kranz.yaml" {
		t.Fatalf("config paths = %#v, want target runtime config path", model.configPaths)
	}
	if model.operation != "" || model.operationCancel != nil || oldOperationCanceled {
		t.Fatalf("old operation leaked or was canceled: operation=%q cancelSet=%v canceled=%v",
			model.operation, model.operationCancel != nil, oldOperationCanceled)
	}
	if len(model.logEntries) != 0 {
		t.Fatalf("old runtime log cache leaked into target: %#v", model.logEntries)
	}
	secondGeneration := model.sessionGeneration
	model.panelFocus = panelLogs

	switchTo(t, model, alpha.record)
	if model.cfg.Project != "Alpha" {
		t.Fatalf("after switching back, project = %q, want Alpha", model.cfg.Project)
	}
	if model.sessionGeneration == secondGeneration {
		t.Fatal("sessionGeneration did not advance on the second switch")
	}
	if model.panelFocus != panelDetails || !model.wrapLogs {
		t.Fatalf("alpha's UI state was not restored on return: panelFocus=%v wrapLogs=%v", model.panelFocus, model.wrapLogs)
	}
	if model.runTarget != app.ServiceRunTarget("api") || model.runMode != runViewSingle || model.selectedRun != 1 {
		t.Fatalf("alpha's run selection was not restored: target=%#v runMode=%v selectedRun=%d",
			model.runTarget, model.runMode, model.selectedRun)
	}
	if model.logOffset != 17 || model.logAnchor != 4 {
		t.Fatalf("alpha's run history scroll position was not restored: offset=%d anchor=%d",
			model.logOffset, model.logAnchor)
	}

	switchTo(t, model, beta.record)
	if model.panelFocus != panelLogs {
		t.Fatalf("beta's second-visit state was not restored: panelFocus=%v", model.panelFocus)
	}
}

// TestRuntimeSwitcherStaleSwitchResultIsDroppedAndConnectionRetired verifies
// the seq guard PRD 3.3 requires: a switchTargetMsg superseded by a newer
// attempt must never mutate the dashboard, and the connection it already
// opened must still be closed rather than leaked.
func TestRuntimeSwitcherStaleSwitchResultIsDroppedAndConnectionRetired(t *testing.T) {
	registry := testRegistry(t)
	alpha := startTestRuntime(t, registry, "stale-alpha", emptyProjectConfig("Alpha"), t.TempDir())
	beta := startTestRuntime(t, registry, "stale-beta", emptyProjectConfig("Beta"), t.TempDir())

	model := newSwitchableTestModel(t, registry, alpha, ModelOptions{})

	// Stand in for an attempt that dialed and handshook beta successfully,
	// whose result is about to arrive after a newer attempt superseded it.
	staleClient, err := kranzruntime.DialWithIdentity(beta.record.Socket, "test",
		kranzruntime.ClientIdentity{Surface: "tui", Label: "stale"})
	if err != nil {
		t.Fatalf("dial beta directly: %v", err)
	}
	staleMsg := switchTargetMsg{seq: model.switchSeq, record: beta.record, client: staleClient, cfg: staleClient.Config()}
	model.switchSeq++ // a newer attempt started and will resolve on its own

	fireOnce(model, func() tea.Msg { return staleMsg })
	if model.cfg.Project != "Alpha" {
		t.Fatalf("stale switch result changed the dashboard to %q", model.cfg.Project)
	}
	select {
	case <-staleClient.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the stale attempt's connection was never closed (leaked)")
	}
}

// TestRuntimeLossRecoveryChooseRunningRuntime covers PRD 3.5's second
// recovery branch: the current runtime's connection ends, and the user
// picks another already-running one from the embedded list.
func TestRuntimeLossRecoveryChooseRunningRuntime(t *testing.T) {
	registry := testRegistry(t)
	alpha := startTestRuntime(t, registry, "lost-alpha", emptyProjectConfig("Alpha"), t.TempDir())
	beta := startTestRuntime(t, registry, "lost-beta", emptyProjectConfig("Beta"), t.TempDir())

	model := newSwitchableTestModel(t, registry, alpha, ModelOptions{})
	doneCmd := model.watchCurrentClientDone()
	if doneCmd == nil {
		t.Fatal("watchCurrentClientDone produced no command")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- doneCmd() }()

	alpha.stop() // simulate the runtime process exiting

	var msg tea.Msg
	select {
	case msg = <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("currentRuntimeLostMsg never arrived after the runtime stopped")
	}
	if _, next := model.Update(msg); next != nil {
		fireOnce(model, next)
	}
	if model.mode != ModeRuntimeLost {
		t.Fatalf("mode = %v, want ModeRuntimeLost", model.mode)
	}

	_, cmd := model.handleRuntimeLostKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if !model.recoveryShowingList {
		t.Fatal("'c' did not open the running-runtime list")
	}
	fireOnce(model, cmd)
	betaIndex := -1
	for index, row := range model.switcherRows {
		if row.Record.ID == beta.record.ID {
			betaIndex = index
		}
	}
	if betaIndex < 0 {
		t.Fatalf("beta not discovered during recovery: %#v", model.switcherRows)
	}
	model.switcherCursor = betaIndex
	_, connectCmd := model.connectToSwitcherSelection()
	fireOnce(model, connectCmd)

	if model.mode != ModeNormal {
		t.Fatalf("mode after recovering to beta = %v, want ModeNormal", model.mode)
	}
	if model.cfg.Project != "Beta" {
		t.Fatalf("project after recovery = %q, want Beta", model.cfg.Project)
	}
}

// TestRuntimeLossRecoveryRestartSameProject covers PRD 3.5's first recovery
// branch: restarting the same project after its runtime stopped, using a
// fake background launcher that stands in for the real spawnBackground
// mechanism (that mechanism itself — argv-only, no shell — is exercised by
// cmd/kranz's own tests).
func TestRuntimeLossRecoveryRestartSameProject(t *testing.T) {
	registry := testRegistry(t)
	directory := t.TempDir()
	alpha := startTestRuntime(t, registry, "restart-alpha", emptyProjectConfig("Alpha"), directory)

	var restarted *testRuntime
	model := newSwitchableTestModel(t, registry, alpha, ModelOptions{
		RestartRuntime: func(dir string, configPaths []string) error {
			restarted = startTestRuntime(t, registry, "restart-alpha", emptyProjectConfig("Alpha"), dir)
			return nil
		},
	})

	doneCmd := model.watchCurrentClientDone()
	result := make(chan tea.Msg, 1)
	go func() { result <- doneCmd() }()
	alpha.stop()
	var msg tea.Msg
	select {
	case msg = <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("currentRuntimeLostMsg never arrived")
	}
	model.Update(msg)
	if model.mode != ModeRuntimeLost {
		t.Fatalf("mode = %v, want ModeRuntimeLost", model.mode)
	}

	_, cmd := model.handleRuntimeLostKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("restart produced no command")
	}
	fireOnce(model, cmd)

	if model.recoveryErr != "" {
		t.Fatalf("restart failed: %s", model.recoveryErr)
	}
	if model.mode != ModeNormal {
		t.Fatalf("mode after restart = %v, want ModeNormal", model.mode)
	}
	if model.cfg.Project != "Alpha" {
		t.Fatalf("project after restart = %q, want Alpha", model.cfg.Project)
	}
	if restarted == nil {
		t.Fatal("the fake restart launcher was never invoked")
	}
	if model.sessionID != restarted.record.ID {
		t.Fatalf("sessionID = %q, want the restarted session %q", model.sessionID, restarted.record.ID)
	}
}

// TestClientSurfacesExcludeProbeAndSelf is the protocol-level check PRD 9
// asks for: the switcher's client-surface list never counts the transient
// discovery probe, and never counts the viewer's own connection to the
// runtime it is currently attached to.
func TestClientSurfacesExcludeProbeAndSelf(t *testing.T) {
	registry := testRegistry(t)
	alpha := startTestRuntime(t, registry, "surfaces-alpha", emptyProjectConfig("Alpha"), t.TempDir())

	model := newSwitchableTestModel(t, registry, alpha, ModelOptions{})
	fireOnce(model, model.openRuntimeSwitcher())

	var row runtimeRow
	found := false
	for _, candidate := range model.switcherRows {
		if candidate.Record.ID == alpha.record.ID {
			row, found = candidate, true
		}
	}
	if !found {
		t.Fatalf("alpha not discovered: %#v", model.switcherRows)
	}
	if !row.IsCurrent {
		t.Fatal("alpha should be marked as the current runtime")
	}
	for _, surface := range row.Record.ClientSurfaces {
		if surface == "probe" {
			t.Fatalf("client surfaces leaked the discovery probe: %v", row.Record.ClientSurfaces)
		}
	}
	// The model's own dashboard connection to its current runtime must not
	// inflate that runtime's own surface list (PRD 3.2).
	if len(row.Record.ClientSurfaces) != 0 {
		t.Fatalf("current runtime's own connection was counted as a client surface: %v", row.Record.ClientSurfaces)
	}
}
