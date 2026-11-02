package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/logcommand"
)

func finishLogCommands(t *testing.T, m *Model) {
	t.Helper()
	m.syncLogCommands()
	for slot, view := range m.logCommands {
		if view == nil {
			continue
		}
		select {
		case <-view.process.Done:
		case <-time.After(3 * time.Second):
			t.Fatal("log command did not finish")
		}
		m.handleLogCommandChanged(logCommandChangedMsg{logPanelSlot(slot), view, view.process.Snapshot()})
	}
}

func waitForLogOutput(t *testing.T, m *Model, want string) {
	t.Helper()
	m.syncLogCommands()
	view := m.logCommands[logSlotMain]
	deadline := time.After(3 * time.Second)
	for {
		output := view.process.Snapshot()
		m.handleLogCommandChanged(logCommandChangedMsg{logSlotMain, view, output})
		if strings.Contains(strings.Join(logEntryLines(view.entries), "\n"), want) {
			return
		}
		if output.Err != nil {
			t.Fatal("log processor failed")
		}
		select {
		case <-view.process.Changed:
		case <-deadline:
			t.Fatal("log processor did not emit expected output")
		}
	}
}

func TestLogCommandSelectsRunsBeforeTransformingAndSearchesTheResult(t *testing.T) {
	m := newTestModel()
	defer m.Shutdown()
	m.cfg.UI.Logs = &config.LogCommandConfig{Command: `tr '[:lower:]' '[:upper:]'`}
	m.width, m.height, m.ready = 110, 26, true
	finishTestServiceRun(t, m, "api", 1, "first failure")
	finishTestServiceRun(t, m, "api", 0, "second result")
	m.refreshServices()
	finishLogCommands(t, m)
	if got := strings.Join(m.serviceLogLines(m.FocusedService()), "|"); !strings.Contains(got, "FIRST FAILURE") || !strings.Contains(got, "SECOND RESULT") {
		t.Fatalf("combined output = %q", got)
	}
	m.selectLatestRun()
	finishLogCommands(t, m)
	if got := strings.Join(m.serviceLogLines(m.FocusedService()), "|"); strings.Contains(got, "FIRST FAILURE") || !strings.Contains(got, "SECOND RESULT") {
		t.Fatalf("latest output = %q", got)
	}
	m.moveRun(-1, false)
	finishLogCommands(t, m)
	if got := strings.Join(m.serviceLogLines(m.FocusedService()), "|"); !strings.Contains(got, "FIRST FAILURE") || strings.Contains(got, "SECOND RESULT") {
		t.Fatalf("historical output = %q", got)
	}
	if err := m.logSearcher.SetPattern(`^FIRST FAILURE$`); err != nil {
		t.Fatal(err)
	}
	panel := ansi.Strip(m.renderLogPanel(m.FocusedService(), 100, 12))
	if !strings.Contains(panel, "FILTER /^FIRST FAILURE$/ · 1") || !strings.Contains(panel, "FIRST FAILURE") {
		t.Fatalf("regex did not search displayed text: %s", panel)
	}
	if m.displayedLogLineCount() != 1 {
		t.Fatal("scroll metrics did not use filtered processed output")
	}
	selection := m.captureLogSelection(panelLogs)
	if selection == nil || len(selection.lines) != 1 || selection.lines[0].text != "FIRST FAILURE" {
		t.Fatal("selection did not capture processed filtered text")
	}
	if original := m.entriesForRun(app.ServiceRunTarget("api"), 1); len(original) == 0 || original[len(original)-1].Raw != "first failure" {
		t.Fatal("presentation changed retained source logs")
	}
	epoch := m.logCommandEpoch
	for range 10 {
		_ = m.View()
		m.scrollLogs(-1)
	}
	if m.logCommandEpoch != epoch {
		t.Fatal("rendering or scrolling executed a command")
	}
}

func TestLogCommandsIsolatePinnedWindowsAndRejectStaleResults(t *testing.T) {
	m := newTestModel()
	defer m.Shutdown()
	m.cfg.UI.Logs = &config.LogCommandConfig{Command: `tr '[:lower:]' '[:upper:]'`}
	finishTestServiceRun(t, m, "api", 0, "first")
	finishTestServiceRun(t, m, "api", 0, "second")
	m.refreshServices()
	m.selectLatestRun()
	m.pinnedTarget, m.pinnedRunMode, m.pinnedRun = app.ServiceRunTarget("api"), runViewSingle, 1
	finishLogCommands(t, m)
	if got := strings.Join(logEntryLines(m.pinnedEntries(m.pinnedTarget)), "|"); !strings.Contains(got, "FIRST") || strings.Contains(got, "SECOND") {
		t.Fatal("pinned window received another run")
	}
	old := m.logCommands[logSlotMain]
	m.moveRun(-1, false)
	m.syncLogCommands()
	m.handleLogCommandChanged(logCommandChangedMsg{logSlotMain, old, logcommand.Snapshot{Lines: []string{"STALE"}, Finished: true}})
	finishLogCommands(t, m)
	if strings.Contains(strings.Join(m.activeSearchLines(), "|"), "STALE") {
		t.Fatal("late output was installed into another run")
	}
	if m.logCommands[logSlotMain] == m.logCommands[logSlotPinned] {
		t.Fatal("pinned and focused panels share command state")
	}
	old = m.logCommands[logSlotMain]
	m.sessionGeneration++
	m.syncLogCommands()
	m.handleLogCommandChanged(logCommandChangedMsg{logSlotMain, old, logcommand.Snapshot{Lines: []string{"WRONG SESSION"}, Finished: true}})
	if strings.Contains(strings.Join(m.activeSearchLines(), "|"), "WRONG SESSION") {
		t.Fatal("late output crossed runtime sessions")
	}
}

func TestGlobalRunSwitchRebuildsOnlyTheFocusedProcessor(t *testing.T) {
	m := newTestModel()
	defer m.Shutdown()
	m.cfg.UI.Logs = &config.LogCommandConfig{Command: `tr '[:lower:]' '[:upper:]'`}
	finishTestServiceRun(t, m, "api", 0, "older")
	finishTestServiceRun(t, m, "api", 0, "newer")
	m.refreshServices()
	m.pinnedTarget, m.pinnedRunMode, m.pinnedRun = app.ServiceRunTarget("api"), runViewSingle, 1
	finishLogCommands(t, m)
	pinned := m.logCommands[logSlotPinned]
	m.toggleAllServiceRunViews()
	finishLogCommands(t, m)
	if got := strings.Join(m.activeSearchLines(), "|"); strings.Contains(got, "OLDER") || !strings.Contains(got, "NEWER") {
		t.Fatal("global latest-run switch retained combined processed output")
	}
	if m.logCommands[logSlotPinned] != pinned {
		t.Fatal("global run switch restarted a pinned processor")
	}
	m.toggleAllServiceRunViews()
	finishLogCommands(t, m)
	if got := strings.Join(m.activeSearchLines(), "|"); !strings.Contains(got, "OLDER") || !strings.Contains(got, "NEWER") {
		t.Fatal("global combined switch did not restore all processed runs")
	}
}

func TestLiveColorCommandKeepsBrowsePauseAndMetadata(t *testing.T) {
	m := newTestModel()
	defer m.Shutdown()
	m.cfg.UI.Logs = &config.LogCommandConfig{Command: `while IFS= read -r line; do printf '\033[31m%s\033[0m\n' "$line"; done`, PreserveLines: true}
	m.width, m.height, m.ready = 100, 14, true
	m.app.SetServiceStatusForTest("api", config.StatusStarting)
	for i := range 30 {
		appendTestLog(m, "api", fmt.Sprintf("record %02d", i))
	}
	m.refreshServices()
	waitForLogOutput(t, m, "record 29")
	view := m.logCommands[logSlotMain]
	first := m.mainLogEntries(app.ServiceRunTarget("api"))[0]
	if first.Timestamp.IsZero() || !strings.Contains(first.Raw, "\x1b[31m") {
		t.Fatal("color command lost capture metadata or ANSI styling")
	}
	m.panelFocus = panelLogs
	m.scrollLogs(-1)
	before := ansi.Strip(m.renderLogPanel(m.FocusedService(), 70, 10))
	before = strings.Join(strings.Split(before, "\n")[1:8], "\n")
	for i := 30; i < 35; i++ {
		appendTestLog(m, "api", fmt.Sprintf("record %02d", i))
	}
	waitForLogOutput(t, m, "record 34")
	after := ansi.Strip(m.renderLogPanel(m.FocusedService(), 70, 10))
	after = strings.Join(strings.Split(after, "\n")[1:8], "\n")
	if before != after || m.followMode || m.logCommands[logSlotMain] != view {
		t.Fatal("new output moved browsing viewport or restarted the processor")
	}
	m.logPaused = true
	appendTestLog(m, "api", "paused output")
	waitForLogOutput(t, m, "paused output")
	if !m.logPaused || m.followMode {
		t.Fatal("processing resumed a paused view")
	}
	if err := m.logSearcher.SetPattern(`^record 12$`); err != nil {
		t.Fatal(err)
	}
	m.searchMode = searchHighlight
	m.currentMatch = m.logSearcher.FindNext(m.activeSearchLines(), -1)
	m.focusActiveLogMatch(m.currentMatch)
	if m.currentMatch < 0 {
		t.Fatal("ANSI interrupted anchored regex matching")
	}
}

func TestLogCommandFailureAndContractViolationFallBackToOriginal(t *testing.T) {
	for _, command := range []config.LogCommandConfig{
		{Command: `printf 'partial\n'; printf 'failed' >&2; exit 3`},
		{Command: `sed -n '1p'`, PreserveLines: true},
	} {
		m := newTestModel()
		m.cfg.UI.Logs = &command
		finishTestServiceRun(t, m, "api", 0, "first\nsecond")
		m.refreshServices()
		finishLogCommands(t, m)
		if !m.logCommands[logSlotMain].failed || !strings.Contains(strings.Join(m.serviceLogLines(m.FocusedService()), "|"), "second") {
			t.Fatal("failed presentation concealed source output")
		}
		m.Shutdown()
	}
}

func TestActionCommandCanExpandOutputWithoutMixingRuns(t *testing.T) {
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "reports", Name: "check"}
	cfg := &config.Config{Project: "Sample", ActionGroups: map[string]config.ActionGroup{"reports": {Actions: map[string]config.Action{"check": {Command: "true"}}}}, UI: config.UIConfig{Logs: &config.LogCommandConfig{Command: `awk '{print toupper($0); print "detail"}'`}}}
	m := NewModel(cfg, "test")
	defer m.Shutdown()
	m.focusedAction = &id
	target := app.ActionRunTarget(id)
	m.runs = []app.RunSummary{{Target: target, Run: 1, FinishedAt: time.Now()}, {Target: target, Run: 2, FinishedAt: time.Now()}}
	m.logEntries[target] = []config.LogEntry{{Sequence: 1, Run: 1, Raw: "old"}, {Sequence: 2, Run: 2, Raw: "new"}}
	m.actionStates[id] = app.ActionResult{ID: id, Run: 2, Status: app.ActionSucceeded}
	m.syncRunTarget()
	finishLogCommands(t, m)
	_, action, state, ok := m.focusedActionDefinition()
	if !ok {
		t.Fatal("action not selected")
	}
	lines := strings.Join(m.actionLogContentView(id, action, state).lines(), "|")
	if !strings.Contains(lines, "NEW|detail") || strings.Contains(lines, "OLD") {
		t.Fatalf("action processed wrong run: %s", lines)
	}
	if err := m.logSearcher.SetPattern(`^detail$`); err != nil {
		t.Fatal(err)
	}
	m.panelFocus = panelLogs
	if m.displayedLogLineCount() != 1 {
		t.Fatal("expanded action output used source row count")
	}
}

func TestTerminalViewerReceivesFilteredRunSnapshotAndRestoresContext(t *testing.T) {
	m := newTestModel()
	defer m.Shutdown()
	m.cfg.UI.Logs = &config.LogCommandConfig{Command: `cat "$KRANZ_LOG_FILE"`, Mode: config.LogCommandTerminal}
	finishTestServiceRun(t, m, "api", 0, "old")
	finishTestServiceRun(t, m, "api", 0, "keep\ndrop")
	m.refreshServices()
	m.selectLatestRun()
	if err := m.logSearcher.SetPattern(`keep`); err != nil {
		t.Fatal(err)
	}
	m.logOffset, m.logAnchor, m.followMode = 3, 10, false
	before := m.currentRunViewportState()
	command, cleanup, err := m.logViewerCommand()
	if err != nil {
		t.Fatal("viewer could not prepare its snapshot")
	}
	defer cleanup()
	var snapshot string
	for _, value := range command.Env {
		if strings.HasPrefix(value, "KRANZ_LOG_FILE=") {
			snapshot = strings.TrimPrefix(value, "KRANZ_LOG_FILE=")
		}
	}
	info, err := os.Stat(snapshot)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("viewer snapshot permissions are not private")
	}
	output, err := command.Output()
	if err != nil || string(output) != "keep\n" {
		t.Fatal("viewer snapshot ignored run selection or regex filter")
	}
	if command.Stdin != nil {
		t.Fatal("viewer stdin was consumed instead of being available for its terminal")
	}
	if m.currentRunViewportState() != before {
		t.Fatal("opening a viewer changed the Kranz viewport")
	}
	cleanup()
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatal("viewer snapshot survived cleanup")
	}
	_, _ = m.Update(logViewerFinishedMsg{})
	if m.currentRunViewportState() != before {
		t.Fatal("returning from a viewer changed the Kranz viewport")
	}
}

func TestLogCommandStylesHaveSafeGeometryForUnicodeTabsAndSelection(t *testing.T) {
	text := "\x1b[31mred\t你好e\u0301 \x1b[1mend\x1b[0m"
	for _, width := range []int{4, 8, 13} {
		for _, wrap := range []bool{false, true} {
			rows := projectedTextRows(text, width, wrap)
			plain := projectedTextRows(ansi.Strip(text), width, wrap)
			if len(rows) != len(plain) || len(rows) != projectedRowCount(text, width, wrap) {
				t.Fatal("styled wrapping and scroll geometry disagree")
			}
			for i, row := range rows {
				if ansi.Strip(row) != ansi.Strip(plain[i]) || lipgloss.Width(row) > width {
					t.Fatal("ANSI changed cell geometry")
				}
				_, spans := logStyleSpans(row)
				if len(spans) > 0 && spans[len(spans)-1].style != "" {
					t.Fatal("style leaked into another panel")
				}
			}
		}
	}
	unsafe := "safe\x1b[2J\x1b[H\x1b]52;c;AAAA\a\x1b[32mgreen\x1b[0m"
	safe := styledReadableLog(unsafe)
	if ansi.Strip(safe) != "safegreen" || strings.Contains(safe, "52;") || strings.Contains(safe, "[2J") || !strings.Contains(safe, "\x1b[32m") {
		t.Fatal("command styling retained terminal side effects or removed colors")
	}
}

func TestTerminalHandoffPreservesTheOutputFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	m := newTestModel()
	defer m.Shutdown()
	m.terminalOutput = NewTerminalOutput(NewTerminalOutput(file))
	command := exec.Command("sh", "-c", "true")
	if m.execTerminalProcess(command, nil) == nil || command.Stdout != file {
		t.Fatal("terminal descriptor was hidden behind a pipe writer")
	}
	if command.Stdin != nil {
		t.Fatal("handoff replaced Bubble Tea's terminal input")
	}
}

func TestLogCommandPreferencesAndExplicitServiceOptOut(t *testing.T) {
	m := newTestModel()
	defer m.Shutdown()
	target := app.ServiceRunTarget("api")
	m.cfg.UI.Logs = &config.LogCommandConfig{Command: "project-tool"}
	if m.effectiveLogCommand(target).Command != "project-tool" {
		t.Fatal("project tool was ignored")
	}
	m.userSettings.Logs = &config.LogCommandConfig{Command: "personal-tool"}
	if m.effectiveLogCommand(target).Command != "personal-tool" {
		t.Fatal("personal preference was ignored")
	}
	service := m.cfg.Services["api"]
	service.UI.Logs = &config.LogCommandConfig{Command: "service-tool"}
	m.cfg.Services["api"] = service
	if m.effectiveLogCommand(target).Command != "service-tool" {
		t.Fatal("service override was ignored")
	}
	service.UI.Logs = &config.LogCommandConfig{}
	m.cfg.Services["api"] = service
	if m.effectiveLogCommand(target).Command != "" {
		t.Fatal("empty service preference did not disable the inherited tool")
	}
}

func TestProcessedRetentionDoesNotLeaveAnEmptyPausedViewport(t *testing.T) {
	m := newTestModel()
	defer m.Shutdown()
	m.width, m.height, m.ready = 100, 20, true
	m.cfg.UI.Logs = &config.LogCommandConfig{Command: "cat"}
	appendTestLog(m, "api", "old output")
	finishLogCommands(t, m)
	view := m.logCommands[logSlotMain]
	m.followMode, m.logPaused, m.logAnchor, m.logOffset = false, true, 1, 0
	m.handleLogCommandChanged(logCommandChangedMsg{logSlotMain, view, logcommand.Snapshot{First: 1, Lines: []string{"retained output"}, Finished: true}})
	if m.logAnchor == 0 || m.followMode || !m.logPaused || !strings.Contains(ansi.Strip(m.renderLogPanel(m.FocusedService(), 70, 10)), "retained output") {
		t.Fatal("evicting the whole old anchor left a blank viewport or resumed following")
	}
}
