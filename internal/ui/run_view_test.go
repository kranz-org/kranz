package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
	if !strings.Contains(plain, "1 NEWER") {
		t.Fatalf("historical run has no newer-runs indicator:\n%s", plain)
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
	if !strings.Contains(plain, "RUN #1 · SNAPSHOT") {
		t.Fatalf("pin identity is not explicit:\n%s", plain)
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
	if model.runMode != runViewSingle || model.selectedRun != 1 || !strings.Contains(plain, "RUN #1 · LIVE/LATEST") || !strings.Contains(plain, "action-output") {
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
