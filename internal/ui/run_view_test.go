package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

func TestSingleRunViewerNavigatesStableServiceRuns(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	finishTestServiceRun(t, model, "api", 1, "first failed output")
	finishTestServiceRun(t, model, "api", 0, "second successful output")
	model.refreshServices()
	model.selectLatestRun()
	if model.selectedRun != 2 || model.runMode != runViewSingle {
		t.Fatalf("latest selection = mode %d run %d", model.runMode, model.selectedRun)
	}
	model.moveRun(-1, true)
	if model.selectedRun != 1 {
		t.Fatalf("previous failed run = %d, want 1", model.selectedRun)
	}
	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 90, 12))
	if !strings.Contains(plain, "first failed output") || strings.Contains(plain, "second successful output") {
		t.Fatalf("single run leaked another run:\n%s", plain)
	}
	if !strings.Contains(plain, "HISTORY · RUN #1 · 1 NEWER · [L] LATEST") {
		t.Fatalf("historical run has no newer-runs indicator:\n%s", plain)
	}
}

func TestRunLabelsSeparateAutoUpdatingLatestAndHistoryViews(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	finishTestServiceRun(t, model, "api", 0, "run one")
	finishTestServiceRun(t, model, "api", 0, "run two")
	model.refreshServices()
	if got := model.runViewLabel(); got != "ALL RUNS · INCLUDES NEW" {
		t.Fatalf("combined label = %q", got)
	}
	model.selectLatestRun()
	if got := model.runViewLabel(); got != "LATEST RUN · #2" {
		t.Fatalf("latest label = %q", got)
	}

	finishTestServiceRun(t, model, "api", 0, "run three")
	model.refreshServices()
	if got := model.runViewLabel(); got != "LATEST RUN · #3" || model.selectedRun != 3 {
		t.Fatalf("latest view did not follow the new run: label=%q run=%d", got, model.selectedRun)
	}
	model.moveRun(-1, false)
	if got := model.runViewLabel(); got != "HISTORY · RUN #2 · 1 NEWER · [L] LATEST" {
		t.Fatalf("explicit history label = %q", got)
	}
	finishTestServiceRun(t, model, "api", 0, "run four")
	model.refreshServices()
	if got := model.runViewLabel(); got != "HISTORY · RUN #2 · 2 NEWER · [L] LATEST" || model.selectedRun != 2 {
		t.Fatalf("history view followed a new run: label=%q run=%d", got, model.selectedRun)
	}
	model.selectLatestRun()
	if got := model.runViewLabel(); got != "LATEST RUN · #4" {
		t.Fatalf("returned latest label = %q", got)
	}
}

func TestCombinedActionOutputScrollsAcrossAllRetainedRuns(t *testing.T) {
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "ops", Name: "history"}
	values := make([]string, 18)
	for index := range values {
		values[index] = fmt.Sprint(index + 1)
	}
	command := "for value in " + strings.Join(values, " ") + "; do echo line-$value; done"
	model := NewModel(&config.Config{Project: "actions", ActionGroups: map[string]config.ActionGroup{
		"ops": {Actions: map[string]config.Action{"history": {Command: command}}},
	}}, "test")
	defer model.Shutdown()
	for range 2 {
		if _, err := model.app.RunAction(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	model.focusedAction = &id
	model.width, model.height, model.ready = 100, 14, true
	model.panelFocus = panelLogs
	model.syncRunTarget()
	model.toggleRunView()
	if model.runMode != runViewCombined {
		t.Fatalf("action view mode = %v, want combined", model.runMode)
	}

	target := app.ActionRunTarget(id)
	latestEntries := len(model.entriesForRun(target, 2))
	allEntries := len(model.entriesForRun(target, 0))
	displayedRows := model.displayedLogLineCount()
	if allEntries <= latestEntries || displayedRows <= latestEntries {
		t.Fatalf("combined rows = %d from %d entries, latest has %d", displayedRows, allEntries, latestEntries)
	}
	for range displayedRows + 10 {
		model.scrollLogs(-1)
	}
	wantOffset := max(0, displayedRows-(model.currentLogPanelHeight()-2))
	if model.logOffset != wantOffset || model.logOffset <= latestEntries {
		t.Fatalf("combined scroll stopped at %d, want %d across older runs (latest entries %d)", model.logOffset, wantOffset, latestEntries)
	}
}

func TestRunListIncludesRetentionAndProvenanceFields(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 140, 30, true
	finishTestServiceRun(t, model, "api", 7, "failed output")
	model.refreshServices()
	model.openRunList()
	plain := ansi.Strip(model.renderRunListView())
	for _, expected := range []string{"#1", "exit 7", "runtime", "complete", "Filter: all"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("run list missing %q:\n%s", expected, plain)
		}
	}
}

func TestRunListFiltersAndSelectsByKeyboardAndMouse(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 140, 30, true
	finishTestServiceRun(t, model, "api", 7, "failed output")
	finishTestServiceRun(t, model, "api", 0, "successful output")
	model.refreshServices()
	model.openRunList()

	for range 3 { // all -> running -> succeeded -> failed
		_, _ = model.handleRunListKeys(tea.KeyMsg{Type: tea.KeyTab})
	}
	filtered := model.filteredRunList()
	if len(filtered) != 1 || filtered[0].Run != 1 {
		t.Fatalf("failed filter = %#v", filtered)
	}
	_, _ = model.handleRunListKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if model.mode != ModeNormal || model.runMode != runViewSingle || model.selectedRun != 1 {
		t.Fatalf("keyboard selection = mode %d view %d run %d", model.mode, model.runMode, model.selectedRun)
	}

	model.runStatusFilter = "all"
	model.openRunList()
	clickRenderedText(t, model, "#2")
	if model.mode != ModeNormal || model.runMode != runViewSingle || model.selectedRun != 2 {
		t.Fatalf("mouse selection = mode %d view %d run %d", model.mode, model.runMode, model.selectedRun)
	}
}

func TestPinnedHistoricalRunRemainsImmutableAfterNewStart(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	finishTestServiceRun(t, model, "api", 0, "run one snapshot")
	finishTestServiceRun(t, model, "api", 0, "run two output")
	model.refreshServices()
	model.selectLatestRun()
	model.moveRun(-1, false)
	model.togglePinnedLog()
	finishTestServiceRun(t, model, "api", 0, "run three output")
	model.refreshServices()

	plain := ansi.Strip(model.renderPinnedRunPanel(model.pinnedTarget, 100, 12))
	if !strings.Contains(plain, "run one snapshot") || strings.Contains(plain, "run two output") || strings.Contains(plain, "run three output") {
		t.Fatalf("pinned run changed after newer starts:\n%s", plain)
	}
	if !strings.Contains(plain, "FROZEN · RUN #1") || !strings.Contains(plain, "Shift+3 UNPIN") {
		t.Fatalf("pin identity is not explicit:\n%s", plain)
	}
}

func TestFocusedPinnedPanelCanAlwaysBeUnpinned(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 28, true
	finishTestServiceRun(t, model, "api", 0, "pinned history")
	model.refreshServices()
	model.selectLatestRun()
	model.togglePinnedLog()
	model.moveFocus(1) // The current panel now points at another service.
	model.panelFocus = panelPinnedLogs
	model.notifMu.Lock()
	model.toastMessage = ""
	model.notifMu.Unlock()

	panel := ansi.Strip(model.renderLogColumn(80, model.height-2))
	footer := ansi.Strip(model.contextMessage())
	if !strings.Contains(panel, "Shift+3 UNPIN") || !strings.Contains(footer, "[Shift+3] unpin") {
		t.Fatalf("unpin control is not visible:\npanel=%s\nfooter=%s", panel, footer)
	}
	pressKey(model, '#')
	if model.hasPinnedRunView() || model.panelFocus != panelLogs {
		t.Fatalf("focused pinned panel was not closed: pinned=%v focus=%v", model.hasPinnedRunView(), model.panelFocus)
	}
}

func TestEachRunRestoresItsOwnScrollFollowAndSearchState(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	finishTestServiceRun(t, model, "api", 0, "run one")
	finishTestServiceRun(t, model, "api", 0, "run two")
	model.refreshServices()
	model.selectLatestRun()
	model.logOffset, model.logAnchor, model.followMode = 4, 9, false
	_ = model.logSearcher.SetPattern("two")
	model.searchMode = searchHighlight

	model.moveRun(-1, false)
	if model.logOffset != 0 || !model.followMode || model.logSearcher.HasPattern() {
		t.Fatalf("new run inherited viewport: offset=%d follow=%v pattern=%q", model.logOffset, model.followMode, model.logSearcher.Pattern())
	}
	model.logOffset, model.followMode = 1, false
	_ = model.logSearcher.SetPattern("one")
	model.moveRun(1, false)
	if model.logOffset != 4 || model.logAnchor != 9 || model.followMode || model.logSearcher.Pattern() != "two" || model.searchMode != searchHighlight {
		t.Fatalf("run two viewport was not restored: offset=%d anchor=%d follow=%v pattern=%q mode=%d",
			model.logOffset, model.logAnchor, model.followMode, model.logSearcher.Pattern(), model.searchMode)
	}
}

func TestPinnedAndCurrentPanelsKeepIndependentSearchState(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.app.AppendLogForTest("api", "API only")
	model.togglePinnedLog()
	model.moveFocus(1)
	model.app.AppendLogForTest("worker", "WORKER only")
	model.panelFocus = panelPinnedLogs
	_ = model.pinnedSearcher.SetPattern("API")
	model.pinnedSearchMode = searchFilter
	model.panelFocus = panelLogs
	_ = model.logSearcher.SetPattern("WORKER")
	model.searchMode = searchFilter

	rendered := ansi.Strip(model.renderLogColumn(100, 24))
	for _, expected := range []string{"FILTER /API/ · 1", "API only", "FILTER /WORKER/ · 1", "WORKER only"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("independent panel search missing %q:\n%s", expected, rendered)
		}
	}
}

func TestActionOutputDefaultsToSingleLatestRun(t *testing.T) {
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "ops", Name: "check"}
	model := NewModel(&config.Config{Project: "actions", ActionGroups: map[string]config.ActionGroup{
		"ops": {Actions: map[string]config.Action{"check": {Command: "printf action-output"}}},
	}}, "test")
	defer model.Shutdown()
	if _, err := model.app.RunAction(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	model.focusedAction = &id
	plain := ansi.Strip(model.renderActionLogPanel(100, 12))
	if model.runMode != runViewSingle || model.selectedRun != 1 || !strings.Contains(plain, "LATEST RUN · #1") || !strings.Contains(plain, "action-output") {
		t.Fatalf("action did not open latest single run: mode=%d run=%d\n%s", model.runMode, model.selectedRun, plain)
	}
}

func TestRunExportFileIncludesIdentityProvenanceAndTruncationMetadata(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	finishTestServiceRun(t, model, "api", 0, "export me")
	model.refreshServices()
	model.selectLatestRun()
	directory := t.TempDir()
	model.workingDirectory = directory
	model.openRunExport()
	model.exportInput.SetValue("selected.log")
	_, command := model.handleRunExportKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("file export returned no command")
	}
	message := command()
	_, _ = model.Update(message)
	payload, err := os.ReadFile(filepath.Join(directory, "selected.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, expected := range []string{"Kranz run: api#1", "Surface: runtime", "Output: complete", "Retention: oldest #1", "[seq=", "export me"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("export missing %q:\n%s", expected, text)
		}
	}
}

func finishTestServiceRun(t *testing.T, model *Model, name string, exitCode int, output string) {
	t.Helper()
	model.app.SetServiceStatusForTest(name, config.StatusStarting)
	model.app.AppendLogForTest(name, output)
	snapshot, ok := model.app.Service(name)
	if !ok {
		t.Fatal("service disappeared")
	}
	state := snapshot.State
	state.Completed = true
	state.ExitCode = exitCode
	model.app.SetServiceStateForTest(name, state)
	model.app.SetServiceStatusForTest(name, config.StatusStopped)
}
