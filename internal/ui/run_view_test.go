package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	if !strings.Contains(plain, "RUN #1/2 · [L] LATEST") {
		t.Fatalf("historical run has no newer-runs indicator:\n%s", plain)
	}
}

func TestRunLabelsSeparateAutoUpdatingLatestAndHistoryViews(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	finishTestServiceRun(t, model, "api", 0, "run one")
	finishTestServiceRun(t, model, "api", 0, "run two")
	model.refreshServices()
	if got := model.runViewLabel(); got != "ALL RUNS" {
		t.Fatalf("combined label = %q", got)
	}
	model.selectLatestRun()
	if got := model.runViewLabel(); got != "RUN #2/2" {
		t.Fatalf("latest label = %q", got)
	}

	finishTestServiceRun(t, model, "api", 0, "run three")
	model.refreshServices()
	if got := model.runViewLabel(); got != "RUN #3/3" || model.selectedRun != 3 {
		t.Fatalf("latest view did not follow the new run: label=%q run=%d", got, model.selectedRun)
	}
	model.moveRun(-1, false)
	if got := model.runViewLabel(); got != "RUN #2/3 · [L] LATEST" {
		t.Fatalf("explicit history label = %q", got)
	}
	finishTestServiceRun(t, model, "api", 0, "run four")
	model.refreshServices()
	if got := model.runViewLabel(); got != "RUN #2/4 · [L] LATEST" || model.selectedRun != 2 {
		t.Fatalf("history view followed a new run: label=%q run=%d", got, model.selectedRun)
	}
	model.selectLatestRun()
	if got := model.runViewLabel(); got != "RUN #4/4" {
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
	model.refreshServices()
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
	for _, expected := range []string{"RUN", "STATUS", "START", "DURATION", "EXIT", "REASON", "INITIATOR", "OUTPUT", "#1", "7", "runtime", "complete", "Run history · api"} {
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

	// Tab must only ever offer a filter this target's history can satisfy.
	// A service that exits cleanly is "stopped", never "succeeded", so a fixed
	// cycle would land on filters whose only result is "No runs match".
	filters := model.runStatusFilters()
	for range len(filters) {
		_, _ = model.handleRunListKeys(tea.KeyMsg{Type: tea.KeyTab})
		if len(model.filteredRunList()) == 0 {
			t.Fatalf("filter %q offered by Tab matches no run in %v", model.runStatusFilter, filters)
		}
	}
	if model.runStatusFilter != runFilterAll {
		t.Fatalf("a full Tab cycle ended on %q, want %q", model.runStatusFilter, runFilterAll)
	}

	for model.runStatusFilter != runFilterFailed {
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

func TestRunListDeletesCompletedRunOnlyAfterConfirmation(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	finishTestServiceRun(t, model, "api", 0, "delete me")
	finishTestServiceRun(t, model, "api", 0, "keep me")
	model.refreshServices()
	model.selectLatestRun()
	model.moveRun(-1, false)
	model.togglePinnedLog()
	model.openRunList()
	_, _ = model.handleRunListKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if model.mode != ModeConfirmDeleteRun || model.deleteRun != 1 {
		t.Fatalf("delete confirmation = mode %d run %d", model.mode, model.deleteRun)
	}
	_, _ = model.handleConfirmDeleteRunKeys(tea.KeyMsg{Type: tea.KeyEsc})
	if len(model.app.Runs()) != 2 || model.mode != ModeRunList {
		t.Fatalf("cancel changed runs or mode: %d, %d", len(model.app.Runs()), model.mode)
	}
	_, _ = model.handleRunListKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	_, _ = model.handleConfirmDeleteRunKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if runs := model.app.Runs(); len(runs) != 1 || runs[0].Run != 2 {
		t.Fatalf("remaining runs = %#v", runs)
	}
	if model.pinnedTarget.Kind != "" || model.pinnedRun != 0 {
		t.Fatalf("deleted pin remains: %+v #%d", model.pinnedTarget, model.pinnedRun)
	}
	for _, entry := range model.app.Logs("api") {
		if entry.Run == 1 {
			t.Fatalf("deleted output remains: %#v", model.app.Logs("api"))
		}
	}
}

func TestRunListRefusesToDeleteActiveRun(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.app.SetServiceStatusForTest("api", config.StatusStarting)
	model.refreshServices()
	model.openRunList()
	_, _ = model.handleRunListKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if model.mode != ModeRunList || len(model.app.Runs()) != 1 || !model.app.Runs()[0].Live {
		t.Fatalf("active delete changed mode or run: mode=%d runs=%#v", model.mode, model.app.Runs())
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
	appendTestLog(model, "api", "API only")
	model.togglePinnedLog()
	model.moveFocus(1)
	appendTestLog(model, "worker", "WORKER only")
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
	model.refreshServices()
	plain := ansi.Strip(model.renderActionLogPanel(100, 12))
	if model.runMode != runViewSingle || model.selectedRun != 1 || !strings.Contains(plain, "check · RUN #1/1") || !strings.Contains(plain, "action-output") {
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
	appendTestLog(model, name, output)
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

func TestRunListWindowsLongHistoryIntoTheTerminal(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 140, 24, true
	for range 40 {
		finishTestServiceRun(t, model, "api", 0, "output")
	}
	model.refreshServices()
	model.openRunList()

	// The modal must fit the terminal on its own. placeOverlay clips instead of
	// scrolling, so an unwindowed list silently loses its footer and cursor.
	rendered := ansi.Strip(model.renderRunListView())
	if height := len(strings.Split(rendered, "\n")); height > model.height {
		t.Fatalf("run list rendered %d rows into a %d-row terminal", height, model.height)
	}
	for _, expected := range []string{"[Enter] Open run", "1/40"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("windowed run list is missing %q:\n%s", expected, rendered)
		}
	}

	// The selected run has to stay visible however far down the history it is.
	for range 39 {
		_, _ = model.handleRunListKeys(tea.KeyMsg{Type: tea.KeyDown})
	}
	rendered = ansi.Strip(model.renderRunListView())
	if !strings.Contains(rendered, "#40") || !strings.Contains(rendered, "40/40") {
		t.Fatalf("cursor scrolled out of the window:\n%s", rendered)
	}
	if !strings.Contains(rendered, "[Enter] Open run") {
		t.Fatalf("windowed run list lost its footer:\n%s", rendered)
	}
}

func TestRunListClickSelectsTheClickedRunNotItsPrefix(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 140, 40, true
	for range 12 {
		finishTestServiceRun(t, model, "api", 0, "output")
	}
	model.refreshServices()
	model.openRunList()

	// "#1" is a prefix of "#12": the click must land on the row it points at.
	clickRenderedText(t, model, "#12")
	if model.selectedRun != 12 {
		t.Fatalf("clicking #12 selected run %d", model.selectedRun)
	}
}

func TestRunListOffersOnlyFiltersThatDivideTheHistory(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 140, 30, true

	// A filter every run matches narrows nothing. With three clean runs the
	// only status is "stopped", so "all" is the whole cycle and Tab is not
	// offered at all.
	for range 3 {
		finishTestServiceRun(t, model, "api", 0, "output")
	}
	model.refreshServices()
	model.openRunList()
	if got := model.runStatusFilters(); len(got) != 1 || got[0] != runFilterAll {
		t.Fatalf("uniform history offers %v, want only %q", got, runFilterAll)
	}
	if strings.Contains(ansi.Strip(model.renderRunListView()), "[Tab]") {
		t.Fatalf("a single-filter list still advertises Tab:\n%s", ansi.Strip(model.renderRunListView()))
	}

	// One failure makes "failed" a real division of the list, and every offered
	// filter must select a proper, non-empty subset.
	finishTestServiceRun(t, model, "api", 3, "boom")
	model.refreshServices()
	filters := model.runStatusFilters()
	if !slices.Contains(filters, runFilterFailed) {
		t.Fatalf("a mixed history does not offer %q: %v", runFilterFailed, filters)
	}
	total := len(model.runsForTarget(model.runTarget))
	for _, filter := range filters[1:] {
		model.runStatusFilter = filter
		if matched := len(model.filteredRunList()); matched == 0 || matched == total {
			t.Fatalf("filter %q selects %d of %d runs and divides nothing", filter, matched, total)
		}
	}
}

func TestRunListShowsRetentionOnlyWhereOutputWasActuallyLost(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 140, 30, true
	finishTestServiceRun(t, model, "api", 0, "output")
	model.refreshServices()
	model.openRunList()

	// Budgets that explain nothing are noise. While the history is intact the
	// modal says nothing about retention.
	plain := ansi.Strip(model.renderRunListView())
	for _, unwanted := range []string{"budgets", "dropped", "missing output", "evicted"} {
		if strings.Contains(plain, unwanted) {
			t.Fatalf("intact history reports retention %q:\n%s", unwanted, plain)
		}
	}
	if notice := model.runRetentionNotice(); notice != "" {
		t.Fatalf("intact history has retention notice %q", notice)
	}

	// Clearing the buffer keeps the summary and drops its output, which is
	// exactly the gap the budgets are there to explain.
	model.app.ClearLogs("api")
	model.refreshServices()
	if notice := model.runRetentionNotice(); !strings.Contains(notice, "missing output") {
		t.Fatalf("lost output is not reported: %q", notice)
	}
	if plain = ansi.Strip(model.renderRunListView()); !strings.Contains(plain, "MB") {
		t.Fatalf("retention notice does not name a readable budget:\n%s", plain)
	}
}

func TestActionRunLabelHoldsItsColumnWhateverTheOutcome(t *testing.T) {
	// Two actions with the same name, so the title prefix is identical in width
	// and only the trailing outcome differs. The status word exists only while
	// a run is live or after it failed; ahead of the run label it shifted the
	// label sideways on every invocation, which is the jump under test.
	good := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "ops", Name: "check"}
	bad := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "qa", Name: "check"}
	model := NewModel(&config.Config{Project: "actions", ActionGroups: map[string]config.ActionGroup{
		"ops": {Actions: map[string]config.Action{"check": {Command: "true"}}},
		"qa":  {Actions: map[string]config.Action{"check": {Command: "exit 3"}}},
	}}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 120, 20, true

	runColumn := func(id config.ActionID) (int, string) {
		t.Helper()
		if _, err := model.app.RunAction(context.Background(), id); err != nil && id == good {
			t.Fatal(err)
		}
		model.focusedAction = &id
		model.refreshServices()
		model.syncRunTarget()
		title := ansi.Strip(model.renderActionLogPanel(110, 8))
		index := strings.Index(title, "RUN #")
		if index < 0 {
			t.Fatalf("title has no run label:\n%s", title)
		}
		// Terminal cells, not bytes: the ✓ and × indicators differ in byte
		// length while occupying the same single column.
		return lipgloss.Width(title[:index]), title
	}

	succeeded, succeededTitle := runColumn(good)
	failed, failedTitle := runColumn(bad)
	if succeeded != failed {
		t.Fatalf("run label sits at column %d when the run succeeded and %d when it failed:\n%s\n%s",
			succeeded, failed, succeededTitle, failedTitle)
	}
	// The word still has to be reachable, just behind the anchor.
	if !strings.Contains(strings.SplitN(failedTitle, "\n", 2)[0], "FAILED") {
		t.Fatalf("a failed run does not name its outcome:\n%s", failedTitle)
	}
	if strings.Contains(strings.SplitN(succeededTitle, "\n", 2)[0], "succeeded") {
		t.Fatalf("a successful run repeats what the indicator already says:\n%s", succeededTitle)
	}
}
