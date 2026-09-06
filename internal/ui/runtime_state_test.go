package ui

import (
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// TestApplyUIStateDropsReferencesRemovedByConfigChange is the PRD 9 unit
// test for "UI state serialization and reconciliation by stable names after
// a config reload": a cached selection, tag, and pin that named a service or
// action gone from the config it is reapplied against must be dropped
// rather than dangling or panicking.
func TestApplyUIStateDropsReferencesRemovedByConfigChange(t *testing.T) {
	before := &config.Config{Project: "Before", Services: map[string]config.Service{
		"api": {Command: "sleep 1", Dir: ".", Shell: "sh", Tags: []string{"backend"},
			Actions: map[string]config.Action{"migrate": {Command: "echo hi"}}},
	}}
	model := NewModel(before, "test")
	defer model.Shutdown()

	actionID := config.ActionID{OwnerKind: config.ActionOwnerService, Owner: "api", Name: "migrate"}
	model.focusedAction = &actionID
	model.selectedTags = []string{"backend"}
	model.selected = map[string]bool{"api": true}
	model.pinnedTarget = app.ActionRunTarget(actionID)

	captured := model.captureUIState()
	if captured.focusedAction == nil || *captured.focusedAction != actionID {
		t.Fatalf("captureUIState lost the focused action: %#v", captured.focusedAction)
	}
	if len(captured.selectedTags) != 1 || captured.selectedTags[0] != "backend" {
		t.Fatalf("captureUIState lost the selected tag: %#v", captured.selectedTags)
	}

	// A config reload (or a switch back to a runtime whose config changed
	// while away) removes the service, its action, and its tag entirely.
	after := &config.Config{Project: "After", Services: map[string]config.Service{}}
	model.cfg = after
	model.app = app.NewLocal(after, nil, app.Options{})
	model.refreshServices()

	model.applyUIState(captured) // must not panic

	if model.focusedAction != nil {
		t.Fatalf("removed action survived reconciliation: %v", *model.focusedAction)
	}
	if len(model.selectedTags) != 0 {
		t.Fatalf("removed tag survived reconciliation: %v", model.selectedTags)
	}
	if model.selected["api"] {
		t.Fatal("removed service's selection survived reconciliation")
	}
	if model.pinnedTarget.Kind != "" || model.pinnedLog != "" {
		t.Fatalf("pinned target referencing a removed action survived: %#v / %q", model.pinnedTarget, model.pinnedLog)
	}
}

// TestApplyUIStateKeepsValidSelectionAcrossAnUnrelatedConfigChange is the
// complementary case: a reload that leaves the referenced service, action,
// and tag in place must not lose the user's selection just because
// something else in the config changed.
func TestApplyUIStateKeepsValidSelectionAcrossAnUnrelatedConfigChange(t *testing.T) {
	cfg := &config.Config{Project: "Stable", Services: map[string]config.Service{
		"api": {Command: "sleep 1", Dir: ".", Shell: "sh", Tags: []string{"backend"}},
	}}
	model := NewModel(cfg, "test")
	defer model.Shutdown()

	model.selectedTags = []string{"backend"}
	model.panelFocus = panelDetails
	model.detailOffset = 7
	model.wrapLogs = true
	model.addNotification("api", "alpha notice", config.LogWarn)
	captured := model.captureUIState()

	model.resetUIStateToDefaults()
	_ = model.logSearcher.SetPattern("wrong-runtime-filter")
	model.applyUIState(captured)

	if len(model.selectedTags) != 1 || model.selectedTags[0] != "backend" {
		t.Fatalf("selectedTags = %v, want [backend] preserved", model.selectedTags)
	}
	if model.panelFocus != panelDetails {
		t.Fatalf("panelFocus = %v, want panelDetails preserved", model.panelFocus)
	}
	if !model.wrapLogs {
		t.Fatal("wrapLogs was not preserved")
	}
	if model.detailOffset != 7 {
		t.Fatalf("detailOffset = %d, want 7 preserved", model.detailOffset)
	}
	if len(model.notifications) != 1 || model.notifications[0].Message != "alpha notice" {
		t.Fatalf("notifications = %#v, want the runtime's own notification history", model.notifications)
	}
}

// TestApplyUIStateZeroValueAppliesDefaults covers a runtime visited for the
// first time in this TUI process: it gets Kranz's ordinary defaults, not a
// zeroed-out or dangling state.
func TestApplyUIStateZeroValueAppliesDefaults(t *testing.T) {
	model := NewModel(&config.Config{Project: "Fresh", Services: map[string]config.Service{}}, "test")
	defer model.Shutdown()
	model.panelFocus = panelPinnedLogs
	model.wrapLogs = true

	model.applyUIState(runtimeUIState{})

	if model.panelFocus != panelServices {
		t.Fatalf("panelFocus = %v, want the default panelServices", model.panelFocus)
	}
	if model.wrapLogs {
		t.Fatal("wrapLogs was not reset to its default")
	}
}

func TestApplyUIStateClearsEmptySearchPatternsAndToast(t *testing.T) {
	model := NewModel(&config.Config{Project: "Fresh", Services: map[string]config.Service{}}, "test")
	defer model.Shutdown()
	_ = model.logSearcher.SetPattern("from-other-runtime")
	_ = model.pinnedSearcher.SetPattern("also-from-other-runtime")
	model.toastMessage, model.toastTimer = "old toast", time.Now()

	model.applyUIState(runtimeUIState{populated: true})

	if model.logSearcher.Pattern() != "" || model.pinnedSearcher.Pattern() != "" {
		t.Fatalf("empty cached patterns did not clear previous runtime filters: %q / %q",
			model.logSearcher.Pattern(), model.pinnedSearcher.Pattern())
	}
	if model.toastMessage != "" || !model.toastTimer.IsZero() {
		t.Fatalf("toast leaked across runtimes: %q at %v", model.toastMessage, model.toastTimer)
	}
}

// TestApplyUIStateRestoresRunSelectionAndViewport covers the run half of the
// PRD 3.4 field list — "the selected run and the run history viewport" — that
// its acceptance criterion sums up as restoring "the selected object, filters,
// pin and scroll position". Restoring the run fields is only half the job:
// syncRunTarget re-derives runTarget from the restored focus, and every field
// that describes which run that target is showing has to survive it.
func TestApplyUIStateRestoresRunSelectionAndViewport(t *testing.T) {
	cfg := &config.Config{Project: "Runs", Services: map[string]config.Service{
		"api": {Command: "sleep 1", Dir: ".", Shell: "sh"},
	}}
	model := NewModel(cfg, "test")
	defer model.Shutdown()
	model.refreshServices()
	model.syncRunTarget()
	model.runs = []app.RunSummary{{Target: app.ServiceRunTarget("api"), Run: 7}}

	model.runMode = runViewSingle
	model.selectedRun = 7
	model.runFollowsLatest = false
	viewportKey := model.runViewportKey()
	model.runViewports[viewportKey] = runViewportState{Offset: 42, Anchor: 9}

	captured := model.captureUIState()

	// Leaving for another runtime and coming back: the model is carrying the
	// other project's run state by the time this one is reapplied.
	model.runMode, model.selectedRun, model.runFollowsLatest = runViewCombined, 0, true
	model.runTarget = app.RunTarget{}
	model.runViewports = make(map[runViewportKey]runViewportState)
	model.logOffset, model.logAnchor = 0, 0

	model.applyUIState(captured)

	if model.runMode != runViewSingle || model.selectedRun != 7 || model.runFollowsLatest {
		t.Fatalf("run selection was not restored: runMode=%v selectedRun=%d followsLatest=%v",
			model.runMode, model.selectedRun, model.runFollowsLatest)
	}
	if model.runTarget != app.ServiceRunTarget("api") {
		t.Fatalf("runTarget = %#v, want the focused service's target", model.runTarget)
	}
	if state, ok := model.runViewports[viewportKey]; !ok || state.Offset != 42 {
		t.Fatalf("run viewport cache was not restored: %#v", model.runViewports)
	}
	if model.logOffset != 42 || model.logAnchor != 9 {
		t.Fatalf("restored viewport was not applied to the live log panel: offset=%d anchor=%d",
			model.logOffset, model.logAnchor)
	}
}

func TestApplyUIStateChoosesClosestRunWhenSavedRunWasRemoved(t *testing.T) {
	cfg := &config.Config{Project: "Runs", Services: map[string]config.Service{
		"api": {Command: "sleep 1", Dir: ".", Shell: "sh"},
	}}
	model := NewModel(cfg, "test")
	defer model.Shutdown()
	model.refreshServices()
	model.syncRunTarget()
	model.runs = []app.RunSummary{
		{Target: app.ServiceRunTarget("api"), Run: 4},
		{Target: app.ServiceRunTarget("api"), Run: 8},
	}

	model.applyUIState(runtimeUIState{
		populated: true, focusedServiceName: "api",
		runMode: runViewSingle, selectedRun: 7,
	})

	if model.selectedRun != 8 {
		t.Fatalf("selectedRun = %d, want closest retained run 8", model.selectedRun)
	}
}
