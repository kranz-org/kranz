package ui

import (
	"testing"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// countingActionAPI records how a refresh reads action state.
type countingActionAPI struct {
	app.API
	batchCalls   int
	singleCalls  int
	batchMissing bool
}

func (c *countingActionAPI) ActionStates() []app.ActionResult {
	c.batchCalls++
	if c.batchMissing {
		return nil
	}
	return c.API.ActionStates()
}

func (c *countingActionAPI) ActionState(id config.ActionID) (app.ActionResult, bool) {
	c.singleCalls++
	return c.API.ActionState(id)
}

// actionModel builds a dashboard whose project actually declares actions, which
// is what makes the round-trip counts below mean anything.
func actionModel(t *testing.T, api *countingActionAPI) *Model {
	t.Helper()
	model := NewModel(&config.Config{
		Project: "Actions",
		Services: map[string]config.Service{
			"api": {
				Command: "exit 0", Dir: ".", Shell: "sh",
				Actions: map[string]config.Action{
					"build": {Command: "exit 0"},
					"lint":  {Command: "exit 0"},
					"test":  {Command: "exit 0"},
				},
				ActionOrder: []string{"build", "lint", "test"},
			},
		},
	}, "test")
	if len(model.cfg.ActionIDs()) != 3 {
		t.Fatalf("test project declares %d actions, want 3", len(model.cfg.ActionIDs()))
	}
	api.API = model.app
	model.app = api
	return model
}

// Every poll refreshes all action state. Reading it one action at a time costs
// a runtime round trip per action, which a project with a couple of dozen
// actions pays several times a second for a dashboard doing nothing.
func TestActionStateRefreshUsesOneRoundTrip(t *testing.T) {
	api := &countingActionAPI{}
	model := actionModel(t, api)

	model.refreshActionStates()

	if api.batchCalls != 1 {
		t.Fatalf("batch reads = %d, want exactly one", api.batchCalls)
	}
	if api.singleCalls != 0 {
		t.Fatalf("per-action reads = %d, want none", api.singleCalls)
	}
	for _, id := range model.cfg.ActionIDs() {
		if _, ok := model.actionStates[id]; !ok {
			t.Fatalf("action %v missing from the refreshed state", id)
		}
	}
}

// A runtime started by an older Kranz does not know the batch method. The
// dashboard must still show real action state rather than claiming every
// action has never run.
func TestActionStateRefreshFallsBackForOlderRuntime(t *testing.T) {
	api := &countingActionAPI{batchMissing: true}
	model := actionModel(t, api)

	model.refreshActionStates()

	if api.singleCalls != len(model.cfg.ActionIDs()) {
		t.Fatalf("per-action reads = %d, want one per configured action", api.singleCalls)
	}
	for _, id := range model.cfg.ActionIDs() {
		if _, ok := model.actionStates[id]; !ok {
			t.Fatalf("action %v missing after fallback", id)
		}
	}
}
