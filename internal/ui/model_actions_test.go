package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

func TestEnterExpandsOwnerAndRunsFocusedAction(t *testing.T) {
	model := NewModel(&config.Config{Project: "Keys", Services: map[string]config.Service{
		"app": {Command: "run", Actions: map[string]config.Action{
			"check": {Command: "exit 0", Shell: "/bin/sh", Dir: t.TempDir()},
		}},
	}}, "test")
	defer model.Shutdown()

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if command != nil || len(model.serviceListRows()) != 2 {
		t.Fatalf("Enter did not expand actions: command %v, rows %#v", command, model.serviceListRows())
	}
	model.moveServiceListCursor(1)
	_, command = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("Enter did not schedule focused action")
	}
	_, _ = model.Update(command())
	state, _ := model.manager.ActionState(*model.focusedAction)
	if state.Status != service.ActionSucceeded {
		t.Fatalf("keyboard action state = %#v", state)
	}
}

func TestServiceActionsExpandNavigateRunAndRenderOutput(t *testing.T) {
	model := NewModel(&config.Config{Project: "Actions", Services: map[string]config.Service{
		"app": {Command: "sleep 10", Actions: map[string]config.Action{
			"inspect": {
				Command:     "printf 'hello\\n'; printf 'warning\\n' >&2",
				Description: "Inspect the application",
				Shell:       "/bin/sh",
				Dir:         t.TempDir(),
			},
		}},
	}}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 30, true

	if command, handled := model.openFocusedListItem(); !handled || command != nil {
		t.Fatalf("expand service = handled %v, command %v", handled, command)
	}
	rows := model.serviceListRows()
	if len(rows) != 2 || rows[0].Kind != actionRowService || rows[1].Kind != actionRowAction {
		t.Fatalf("expanded rows = %#v", rows)
	}
	model.moveServiceListCursor(1)
	id, _, state, exists := model.focusedActionDefinition()
	if !exists || id.Name != "inspect" || state.Status != service.ActionReady {
		t.Fatalf("focused action = %#v, %#v, %v", id, state, exists)
	}

	command, handled := model.openFocusedListItem()
	if !handled || command == nil {
		t.Fatalf("run action = handled %v, command %v", handled, command)
	}
	message := command()
	if _, ok := message.(actionResultMsg); !ok {
		t.Fatalf("action command message = %#v", message)
	}
	_, _ = model.Update(message)

	_, _, state, _ = model.focusedActionDefinition()
	if state.Status != service.ActionSucceeded || state.ExitCode != 0 {
		t.Fatalf("completed action state = %#v", state)
	}
	list := ansi.Strip(model.renderServicePanel(60, 8))
	if !strings.Contains(list, "⚡ ✓ inspect") || !strings.Contains(list, "succeeded") {
		t.Fatalf("action list does not expose result:\n%s", list)
	}
	details := ansi.Strip(model.renderActionDetails(60, 20))
	for _, expected := range []string{"Inspect the application", "exit 0", "printf 'hello"} {
		if !strings.Contains(details, expected) {
			t.Fatalf("action details missing %q:\n%s", expected, details)
		}
	}
	output := ansi.Strip(model.renderActionLogPanel(60, 10))
	for _, expected := range []string{"hello", "[stderr] warning", "succeeded"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("action output missing %q:\n%s", expected, output)
		}
	}
}

func TestActionOnlyGroupIsFocusableAndRunnable(t *testing.T) {
	model := NewModel(&config.Config{Project: "Tools", ActionGroups: map[string]config.ActionGroup{
		"tools": {
			Description: "Project tools",
			Actions: map[string]config.Action{
				"version": {Command: "printf 'v1\\n'", Shell: "/bin/sh", Dir: t.TempDir()},
			},
		},
	}}, "test")
	defer model.Shutdown()

	if model.focusedActionGroup != "tools" {
		t.Fatalf("initial group focus = %q", model.focusedActionGroup)
	}
	if command, handled := model.openFocusedListItem(); !handled || command != nil {
		t.Fatalf("expand group = handled %v, command %v", handled, command)
	}
	model.moveServiceListCursor(1)
	if model.focusedAction == nil || model.focusedAction.OwnerKind != config.ActionOwnerGroup {
		t.Fatalf("focused group action = %#v", model.focusedAction)
	}
	command, handled := model.openFocusedListItem()
	if !handled || command == nil {
		t.Fatalf("run group action = handled %v, command %v", handled, command)
	}
	_, _ = model.Update(command())
	_, _, state, exists := model.focusedActionDefinition()
	if !exists || state.Status != service.ActionSucceeded || strings.TrimSpace(strings.Join(state.Stdout, "")) != "v1" {
		t.Fatalf("group action state = %#v, %v", state, exists)
	}
}

func TestProtectedActionsWaitForLaterHandoffOrConfirmation(t *testing.T) {
	confirmation := true
	interactive := true
	model := NewModel(&config.Config{Project: "Protected", Services: map[string]config.Service{
		"app": {Command: "run", Actions: map[string]config.Action{
			"confirm":     {Command: "exit 0", Confirm: &confirmation},
			"interactive": {Command: "sh", Interactive: &interactive},
		}},
	}}, "test")
	defer model.Shutdown()
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerService, "app")] = true

	model.focusServiceListRow(1)
	if command, handled := model.openFocusedListItem(); !handled || command != nil {
		t.Fatalf("confirmation action = handled %v, command %v", handled, command)
	}
	model.focusServiceListRow(2)
	if command, handled := model.openFocusedListItem(); !handled || command != nil {
		t.Fatalf("interactive action = handled %v, command %v", handled, command)
	}
	for _, id := range model.cfg.ActionIDs() {
		state, _ := model.manager.ActionState(id)
		if state.Status != service.ActionReady {
			t.Fatalf("protected action %s state = %#v", id.Name, state)
		}
	}
}
