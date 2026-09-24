package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// The scroll position and the "n–m/total" counter are computed from cached row
// counts for lines nobody has styled yet. If counting ever disagreed with
// rendering, the viewport would silently drift away from the output.
func TestLogRowCountMatchesRenderedRows(t *testing.T) {
	model := newTestModel()
	entries := []config.LogEntry{
		{Sequence: 1, Raw: "short line"},
		{Sequence: 2, Raw: strings.Repeat("a very long word sequence ", 12)},
		{Sequence: 3, Raw: "first segment\nsecond segment\nthird segment"},
		{Sequence: 4, Raw: "ключи и юникод — ширина считается по графемам 🚀🚀🚀"},
		{Sequence: 5, Raw: "ERROR something failed\n\ttrailing indent"},
		{Sequence: 6, Timestamp: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC), Raw: "with a timestamp"},
		{Sequence: 7, Raw: ""},
	}
	for _, wrap := range []bool{false, true} {
		for _, showTime := range []bool{false, true} {
			for _, width := range []int{1, 5, 20, 78, 200} {
				model.wrapLogs, model.showLogTime = wrap, showTime
				for _, entry := range entries {
					counted := model.countLogEntryRows(entry, width)
					rendered := len(model.logEntryVisualRows(entry, width))
					if counted != rendered {
						t.Fatalf("wrap=%v time=%v width=%d seq=%d: counted %d rows, rendered %d",
							wrap, showTime, width, entry.Sequence, counted, rendered)
					}
				}
			}
		}
	}
}

// The cache is keyed by log sequence, so it must be dropped whenever anything
// that changes the answer changes. A stale count would misplace the viewport.
func TestLogRowMetricsInvalidateOnLayoutChange(t *testing.T) {
	model := newTestModel()
	target := model.visibleLogTargets()
	if len(target) == 0 {
		t.Skip("no visible log target")
	}
	first := model.logRowMetricsFor(logSlotMain, target[0], 80)
	if same := model.logRowMetricsFor(logSlotMain, target[0], 80); same != first {
		t.Fatal("an unchanged layout must reuse the cache")
	}
	if resized := model.logRowMetricsFor(logSlotMain, target[0], 120); resized == first {
		t.Fatal("a width change must invalidate the cache")
	}
	model.wrapLogs = !model.wrapLogs
	if wrapped := model.logRowMetricsFor(logSlotMain, target[0], 120); wrapped.wrap != model.wrapLogs {
		t.Fatal("a wrap change must invalidate the cache")
	}
}

func TestLogRowMetricsForgetHistoryBehindSyntheticMarker(t *testing.T) {
	metrics := &logRowMetrics{counts: make(map[uint64]int)}
	for sequence := uint64(1); sequence <= 200; sequence++ {
		metrics.counts[sequence] = 1
	}
	metrics.forget([]config.LogEntry{{Sequence: 0, Raw: "retention marker"}, {Sequence: 180, Raw: "retained"}})
	if len(metrics.counts) != 21 || metrics.counts[180] != 1 {
		t.Fatalf("stale rows remained behind a synthetic marker: %d cached", len(metrics.counts))
	}
}

func TestLogRowMetricsInvalidateOnRuntimeSwitch(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.wrapLogs = true
	target := app.ServiceRunTarget("api")
	first := model.logRowMetricsFor(logSlotMain, target, 12)
	if got := first.totalRows(model, []config.LogEntry{{Sequence: 1, Raw: strings.Repeat("wide ", 20)}}, 12); got <= 1 {
		t.Fatalf("first runtime should wrap across rows, got %d", got)
	}
	model.resetRuntimeDataCaches()
	second := model.logRowMetricsFor(logSlotMain, target, 12)
	if second == first {
		t.Fatal("runtime switch retained row metrics for the previous session")
	}
	if got := second.totalRows(model, []config.LogEntry{{Sequence: 1, Raw: "short"}}, 12); got != 1 {
		t.Fatalf("second runtime row count = %d", got)
	}
}

func TestLogRowWindowTracksRetainedHistoryAndReversesImmediately(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 90, 24, true
	service := model.FocusedService()
	target := app.ServiceRunTarget(service.Name)
	entries := make([]config.LogEntry, 10_000)
	for i := range entries {
		entries[i] = config.LogEntry{Sequence: uint64(i + 1), Raw: fmt.Sprintf("line %d", i)}
	}
	model.logEntries[target] = entries
	model.panelFocus = panelLogs
	model.renderLogPanel(service, 60, 12)
	metrics := model.logRowMetricsFor(logSlotMain, target, 58)
	if got := metrics.totalRows(model, entries, 58); got != len(entries) {
		t.Fatalf("initial row count = %d", got)
	}
	if got := ansi.Strip(strings.Join(model.styleLogRowWindow(logRowWindow{
		entries: entries, selection: len(entries), metrics: metrics, width: 58, start: 500, end: 502,
	}), "\n")); !strings.Contains(got, "line 500") || !strings.Contains(got, "line 501") {
		t.Fatalf("wrong rows at middle of history: %q", got)
	}
	model.scrollLogs(-1)
	if model.logOffset != 1 || model.followMode {
		t.Fatalf("upward wheel did not leave follow mode: offset=%d follow=%v", model.logOffset, model.followMode)
	}
	model.scrollLogs(1)
	if model.logOffset != 0 || !model.followMode {
		t.Fatalf("reverse wheel did not restore follow mode: offset=%d follow=%v", model.logOffset, model.followMode)
	}
	model.logEntries[target] = entries[1:]
	if got := metrics.totalRows(model, entries[1:], 58); got != len(entries)-1 {
		t.Fatalf("evicted row count = %d", got)
	}
}

func TestWrappedActionWindowUsesCachedRowPositions(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.wrapLogs = true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "tools", Name: "report"}
	view := actionLogView{output: make([]cachedActionLogLine, 10_000)}
	for index := range view.output {
		view.output[index] = cachedActionLogLine{sequence: uint64(index + 1), text: fmt.Sprintf("line %d with wrapped output", index)}
	}
	width := 16
	metrics := model.logRowMetricsFor(logSlotMain, app.ActionRunTarget(id), width)
	total := metrics.totalActionRows(view, width)
	if total <= len(view.output) {
		t.Fatalf("wrapped action rows = %d, want more than %d lines", total, len(view.output))
	}
	cachedRows := &metrics.actionRows[0]
	if got := metrics.totalActionRows(view, width); got != total || &metrics.actionRows[0] != cachedRows {
		t.Fatal("stable action history rebuilt its row positions")
	}
	start := metrics.actionRows[5_000]
	rows := styleActionRowWindow(view, metrics.actionRows, nil, view.len(), width, start, start+2, false, nil)
	if got := ansi.Strip(strings.Join(rows, "\n")); !strings.Contains(got, "line 5000") {
		t.Fatalf("middle viewport omitted the selected action line: %q", got)
	}
	view.output = append(view.output, cachedActionLogLine{sequence: 10_001, text: "new output"})
	if got := metrics.totalActionRows(view, width); got <= total {
		t.Fatalf("appended action output did not extend the viewport: %d <= %d", got, total)
	}
}

func TestActionBrowseStaysAnchoredAsOutputGrows(t *testing.T) {
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "tools", Name: "report"}
	cfg := &config.Config{Project: "Action Browsing", ActionGroups: map[string]config.ActionGroup{
		"tools": {Actions: map[string]config.Action{"report": {Command: "true", Shell: "/bin/sh"}}},
	}}
	for _, wrap := range []bool{false, true} {
		model := NewModel(cfg, "test")
		model.width, model.height, model.ready = 90, 24, true
		model.wrapLogs = wrap
		model.focusedAction = &id
		model.panelFocus = panelLogs
		target := app.ActionRunTarget(id)
		for index := range 20 {
			model.actionLogLines[target] = append(model.actionLogLines[target], cachedActionLogLine{
				sequence: uint64(index + 1), text: fmt.Sprintf("line-%02d %s", index, strings.Repeat("x", 70)),
			})
		}
		width := model.width - model.dashboardLeftWidth()
		_ = model.renderActionLogPanel(width, 12)
		model.scrollLogs(-1)
		before := ansi.Strip(model.renderActionLogPanel(width, 12))
		model.actionLogLines[target] = append(model.actionLogLines[target], cachedActionLogLine{sequence: 21, text: "new output"})
		after := ansi.Strip(model.renderActionLogPanel(width, 12))
		body := func(panel string) string {
			lines := strings.Split(panel, "\n")
			return strings.Join(lines[1:], "\n")
		}
		if body(before) != body(after) {
			t.Errorf("wrap=%v: action viewport moved as output grew", wrap)
		}
		model.Shutdown()
	}
}

func TestFocusedActionSearchFiltersAndNavigatesRenderedOutput(t *testing.T) {
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "tools", Name: "report"}
	cfg := &config.Config{Project: "Action Search", ActionGroups: map[string]config.ActionGroup{
		"tools": {Actions: map[string]config.Action{"report": {Command: "true", Shell: "/bin/sh"}}},
	}}
	for _, wrap := range []bool{false, true} {
		model := NewModel(cfg, "test")
		model.width, model.height, model.ready = 90, 24, true
		model.wrapLogs = wrap
		model.focusedAction = &id
		model.panelFocus = panelLogs
		target := app.ActionRunTarget(id)
		model.actionLogLines[target] = []cachedActionLogLine{
			{sequence: 1, text: "ordinary output"},
			{sequence: 2, text: "needle output"},
		}
		if wrap {
			model.actionLogLines[target][1].text = strings.Repeat("needle output ", 12)
		}
		for index := range 30 {
			model.actionLogLines[target] = append(model.actionLogLines[target], cachedActionLogLine{sequence: uint64(index + 3), text: fmt.Sprintf("later output %d", index)})
		}
		if err := model.logSearcher.SetPattern("needle"); err != nil {
			t.Fatal(err)
		}
		model.searchMode = searchFilter
		width := model.width - model.dashboardLeftWidth()
		height := model.currentLogPanelHeight()
		filtered := ansi.Strip(model.renderActionLogPanel(width, height))
		if !strings.Contains(filtered, "needle output") || strings.Contains(filtered, "ordinary output") {
			t.Errorf("wrap=%v: action filter did not select the matching line: %q", wrap, filtered)
		}
		wantRows := 1
		if wrap {
			wantRows = strings.Count(ansi.Hardwrap(styleLogLine(model.actionLogLines[target][1].text), model.currentLogContentWidth(), true), "\n") + 1
		}
		if got := model.displayedLogLineCount(); got != wantRows {
			t.Errorf("wrap=%v: filtered action rows = %d, want %d", wrap, got, wantRows)
		}
		model.scrollLogs(-1)
		if model.logOffset != 0 {
			t.Errorf("wrap=%v: filter allowed scrolling past its only match: %d", wrap, model.logOffset)
		}
		model.searchMode = searchHighlight
		model.focusActiveLogMatch(1)
		highlighted := ansi.Strip(model.renderActionLogPanel(width, height))
		if !strings.Contains(highlighted, "needle output") || !strings.Contains(highlighted, "ordinary output") {
			t.Errorf("wrap=%v: action match was not visible after navigation: %q", wrap, highlighted)
		}
		model.Shutdown()
	}
}

func TestServiceMatchNavigationUsesSelectedRunRows(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height = 90, 16
	svc := model.FocusedService()
	target := app.ServiceRunTarget(svc.Name)
	model.runTarget, model.runMode, model.selectedRun = target, runViewSingle, 2
	model.runs = []app.RunSummary{{Target: target, Run: 1}, {Target: target, Run: 2}}
	for index := range 40 {
		run := uint32(1)
		if index >= 20 {
			run = 2
		}
		model.logEntries[target] = append(model.logEntries[target], config.LogEntry{Sequence: uint64(index + 1), Run: run, Raw: fmt.Sprintf("line %d", index)})
	}
	model.focusLogMatch(0)
	if model.followMode || model.logOffset == 0 {
		t.Fatalf("selected-run match jumped to the bottom: follow=%v offset=%d", model.followMode, model.logOffset)
	}
}

func TestLogFilterWithNoMatchesShowsEmptyState(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 90, 24, true
	service := model.FocusedService()
	model.logEntries[app.ServiceRunTarget(service.Name)] = []config.LogEntry{{Sequence: 1, Raw: "ordinary output"}}
	model.searchMode = searchFilter
	if err := model.logSearcher.SetPattern("absent-pattern"); err != nil {
		t.Fatal(err)
	}
	panel := ansi.Strip(model.renderLogPanel(service, 60, 12))
	if !strings.Contains(panel, "No log lines match this regex") || strings.Contains(panel, "ordinary output") {
		t.Fatalf("incorrect empty filter state: %q", panel)
	}
}

func BenchmarkLogWheelWithRetainedHistory(b *testing.B) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 90, 24, true
	service := model.FocusedService()
	target := app.ServiceRunTarget(service.Name)
	entries := make([]config.LogEntry, 10_000)
	for i := range entries {
		entries[i] = config.LogEntry{Sequence: uint64(i + 1), Raw: fmt.Sprintf("line %d", i)}
	}
	model.logEntries[target] = entries
	model.panelFocus = panelLogs
	panelWidth := model.width - model.dashboardLeftWidth()
	model.renderLogPanel(service, panelWidth, 12)
	b.ResetTimer()
	for range b.N {
		model.scrollLogs(-1)
		model.renderLogPanel(service, panelWidth, 12)
		model.scrollLogs(1)
		model.renderLogPanel(service, panelWidth, 12)
	}
}

// View skips its fitting pass whenever the dashboard reports that it already
// fills the terminal. The saving only materialises if that stays true, so the
// dashboard has to keep assembling an exactly sized frame.
func TestDashboardFillsTerminalExactly(t *testing.T) {
	for _, size := range [][2]int{{64, 14}, {80, 24}, {120, 40}, {200, 60}, {321, 47}} {
		model := newTestModel()
		_, _ = model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for i := range 50 {
			appendTestLog(model, "api", fmt.Sprintf("line %d", i))
		}
		block := model.renderDashboardBlock(model.renderStatusBar())
		if block.width != size[0] || len(block.lines) != size[1] {
			t.Fatalf("%dx%d: dashboard assembled %dx%d", size[0], size[1], block.width, len(block.lines))
		}
		for index, line := range block.lines {
			if rendered := ansi.StringWidth(line); rendered != size[0] {
				t.Fatalf("%dx%d: line %d is %d cells wide", size[0], size[1], index, rendered)
			}
		}
	}
}
