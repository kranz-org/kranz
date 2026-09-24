package ui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

type countingLogAPI struct {
	app.API
	logsCalls       int
	actionLogsCalls int
	queryCalls      int
	cursorCalls     int
	runsCalls       int
}

func (c *countingLogAPI) Logs(name string) []config.LogEntry {
	c.logsCalls++
	return c.API.Logs(name)
}

func (c *countingLogAPI) ActionLogs(id config.ActionID) []config.LogEntry {
	c.actionLogsCalls++
	return c.API.ActionLogs(id)
}

func (c *countingLogAPI) QueryLogs(query app.LogQuery) (app.LogResult, error) {
	c.queryCalls++
	if query.Cursor != "" {
		c.cursorCalls++
	}
	return c.API.QueryLogs(query)
}

func (c *countingLogAPI) Runs() []app.RunSummary {
	c.runsCalls++
	return c.API.Runs()
}

func (c *countingLogAPI) resetCounts() {
	c.logsCalls, c.actionLogsCalls, c.queryCalls, c.cursorCalls, c.runsCalls = 0, 0, 0, 0, 0
}

func TestViewUsesCachedServiceLogsWithoutRuntimeReads(t *testing.T) {
	cfg := &config.Config{Project: "Cache Test", Services: map[string]config.Service{
		"web": {Command: "true", Dir: ".", Shell: "/bin/sh"},
	}}
	local := app.NewLocal(cfg, nil, app.Options{})
	local.AppendLogForTest("web", "ready")
	counting := &countingLogAPI{API: local}
	model := NewModelWithOptions(cfg, "test", ModelOptions{App: counting})
	defer model.Shutdown()
	model.width, model.height, model.ready = 90, 24, true
	counting.resetCounts()

	_ = model.View()
	if counting.logsCalls != 0 || counting.actionLogsCalls != 0 || counting.queryCalls != 0 || counting.runsCalls != 0 {
		t.Fatalf("View performed runtime history reads: service=%d action=%d query=%d runs=%d", counting.logsCalls, counting.actionLogsCalls, counting.queryCalls, counting.runsCalls)
	}
}

func TestActionLogCacheFetchesOnlyCursorDelta(t *testing.T) {
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "tools", Name: "report"}
	cfg := &config.Config{Project: "Cache Test", ActionGroups: map[string]config.ActionGroup{
		"tools": {Actions: map[string]config.Action{"report": {Command: "printf first", Shell: "/bin/sh"}}},
	}}
	local := app.NewLocal(cfg, nil, app.Options{})
	if _, err := local.RunAction(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	counting := &countingLogAPI{API: local}
	model := NewModelWithOptions(cfg, "test", ModelOptions{App: counting})
	defer model.Shutdown()
	model.focusedAction = &id
	target := app.ActionRunTarget(id)
	model.refreshLogCache(target)
	if records := model.cachedActionLogRecords(target, 1); !slices.ContainsFunc(records, func(line cachedActionLogLine) bool { return line.text == "first" }) {
		t.Fatalf("per-run action cache = %#v, want run 1 output", records)
	}
	counting.resetCounts()

	model.refreshLogCache(target)
	if counting.queryCalls != 1 || counting.cursorCalls != 1 {
		t.Fatalf("incremental refresh calls = query %d cursor %d", counting.queryCalls, counting.cursorCalls)
	}
	counting.resetCounts()
	model.width, model.height, model.ready = 90, 24, true
	_ = model.View()
	if counting.logsCalls != 0 || counting.actionLogsCalls != 0 || counting.queryCalls != 0 || counting.runsCalls != 0 {
		t.Fatalf("action View performed runtime history reads: service=%d action=%d query=%d runs=%d", counting.logsCalls, counting.actionLogsCalls, counting.queryCalls, counting.runsCalls)
	}
}

func TestLogCacheDropsStreamsClearedByAnotherClient(t *testing.T) {
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "tools", Name: "report"}
	cfg := &config.Config{
		Project:  "Cache Test",
		Services: map[string]config.Service{"web": {Command: "true", Dir: ".", Shell: "/bin/sh"}},
		ActionGroups: map[string]config.ActionGroup{
			"tools": {Actions: map[string]config.Action{"report": {Command: "printf first", Shell: "/bin/sh"}}},
		},
	}
	local := app.NewLocal(cfg, nil, app.Options{})
	local.AppendLogForTest("web", "ready")
	if _, err := local.RunAction(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	model := NewModelWithOptions(cfg, "test", ModelOptions{App: local})
	defer model.Shutdown()
	serviceTarget := app.ServiceRunTarget("web")
	actionTarget := app.ActionRunTarget(id)
	model.refreshLogCache(serviceTarget)
	model.refreshLogCache(actionTarget)
	if len(model.cachedLogEntries(serviceTarget)) == 0 || len(model.cachedLogEntries(actionTarget)) == 0 {
		t.Fatal("expected both streams to contain output before clearing")
	}
	local.ClearLogs("web")
	local.ClearActionLogs(id)
	model.refreshLogCache(serviceTarget)
	model.refreshLogCache(actionTarget)
	if got := model.cachedLogEntries(serviceTarget); len(got) != 0 {
		t.Fatalf("cleared service output remained cached: %#v", got)
	}
	if got := model.cachedLogEntries(actionTarget); len(got) != 0 {
		t.Fatalf("cleared action output remained cached: %#v", got)
	}
	if got := model.cachedActionLogRecords(actionTarget, 0); len(got) != 0 {
		t.Fatalf("cleared action lines remained cached: %#v", got)
	}
	if got := model.cachedActionLogRecords(actionTarget, 1); len(got) != 0 {
		t.Fatalf("cleared action run lines remained cached: %#v", got)
	}
}

func TestLogCacheReconcilesRunDeletedByAnotherClient(t *testing.T) {
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "tools", Name: "report"}
	cfg := &config.Config{Project: "Cache Test", ActionGroups: map[string]config.ActionGroup{
		"tools": {Actions: map[string]config.Action{"report": {Command: "printf output", Shell: "/bin/sh"}}},
	}}
	local := app.NewLocal(cfg, nil, app.Options{})
	for range 3 {
		if _, err := local.RunAction(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	model := NewModelWithOptions(cfg, "test", ModelOptions{App: local})
	defer model.Shutdown()
	target := app.ActionRunTarget(id)
	model.refreshLogCache(target)
	if !slices.ContainsFunc(model.cachedLogEntries(target), func(entry config.LogEntry) bool { return entry.Run == 2 }) {
		t.Fatal("deleted run was not cached before deletion")
	}
	if _, err := local.DeleteRun(target, 2); err != nil {
		t.Fatal(err)
	}
	model.refreshLogCache(target)
	entries := model.cachedLogEntries(target)
	if slices.ContainsFunc(entries, func(entry config.LogEntry) bool { return entry.Run == 2 }) {
		t.Fatalf("deleted run remained cached: %#v", entries)
	}
	for _, run := range []uint32{1, 3} {
		if !slices.ContainsFunc(entries, func(entry config.LogEntry) bool { return entry.Run == run }) {
			t.Fatalf("retained run %d disappeared from the cache: %#v", run, entries)
		}
	}
}

func TestActionLogCachePreservesBlankCapturedLine(t *testing.T) {
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "tools", Name: "report"}
	cfg := &config.Config{Project: "Cache Test", ActionGroups: map[string]config.ActionGroup{
		"tools": {Actions: map[string]config.Action{"report": {Command: "printf 'first\\n\\nthird\\n'", Shell: "/bin/sh"}}},
	}}
	local := app.NewLocal(cfg, nil, app.Options{})
	if _, err := local.RunAction(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	model := NewModelWithOptions(cfg, "test", ModelOptions{App: local})
	defer model.Shutdown()
	target := app.ActionRunTarget(id)
	model.refreshLogCache(target)
	records := model.cachedActionLogRecords(target, 0)
	var output []string
	for _, record := range records {
		if !strings.HasPrefix(record.text, "[Kranz]") {
			output = append(output, record.text)
		}
	}
	if !slices.Equal(output, []string{"first", "", "third"}) {
		t.Fatalf("captured blank output was lost or trailing output was added: %#v", output)
	}
}
