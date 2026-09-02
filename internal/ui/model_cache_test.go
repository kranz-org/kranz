package ui

import (
	"context"
	"slices"
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
