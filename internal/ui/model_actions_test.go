package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

func TestSStopsRunningAction(t *testing.T) {
	model := NewModel(&config.Config{Project: "Stop", Services: map[string]config.Service{
		"app": {Command: "run", Actions: map[string]config.Action{
			"slow": {Command: "sleep 10", Shell: "/bin/sh", Dir: t.TempDir()},
		}},
	}}, "test")
	defer model.Shutdown()
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerService, "app")] = true
	model.focusServiceListRow(1)
	model.width = 120

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if command == nil {
		t.Fatal("s did not schedule long-running action")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- command() }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := model.manager.ActionState(*model.focusedAction)
		if state.Status == service.ActionRunning && state.PID > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	state, _ := model.manager.ActionState(*model.focusedAction)
	if state.Status != service.ActionRunning || state.PID <= 0 {
		t.Fatalf("long-running action state = %#v", state)
	}
	buttons := model.actionButtons()
	if !strings.Contains(ansi.Strip(buttons[0].rendered), "Stop action: s") {
		t.Fatalf("running action controls = %#v", buttons)
	}
	if output := ansi.Strip(model.renderActionLogPanel(60, 8)); !strings.Contains(output, "press s to stop") {
		t.Fatalf("running action output has stale controls:\n%s", output)
	}
	_, stopCommand := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if stopCommand != nil {
		t.Fatalf("stop action unexpectedly scheduled another command: %v", stopCommand)
	}
	select {
	case message := <-done:
		_, _ = model.Update(message)
	case <-time.After(2 * time.Second):
		t.Fatal("stopped action did not finish")
	}
	state, _ = model.manager.ActionState(*model.focusedAction)
	if state.Status != service.ActionCancelled {
		t.Fatalf("stopped action state = %#v", state)
	}
}

func TestEnterExpandsOwnerAndSRunsFocusedAction(t *testing.T) {
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
	if command != nil {
		t.Fatal("Enter scheduled a focused action")
	}
	state, _ := model.manager.ActionState(*model.focusedAction)
	if state.Status != service.ActionReady {
		t.Fatalf("Enter changed action state = %#v", state)
	}
	if output := ansi.Strip(model.renderActionLogPanel(60, 8)); !strings.Contains(output, "Press s to run") || strings.Contains(output, "Press Enter") {
		t.Fatalf("ready action output has stale controls:\n%s", output)
	}
	model.width = 120
	buttons := model.actionButtons()
	if len(buttons) == 0 || !strings.Contains(ansi.Strip(buttons[0].rendered), "Run action: s") {
		t.Fatalf("focused action controls = %#v", buttons)
	}
	_, command = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if command == nil {
		t.Fatal("s did not schedule focused action")
	}
	_, _ = model.Update(command())
	state, _ = model.manager.ActionState(*model.focusedAction)
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

	command, handled := model.toggleFocusedAction()
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
	if !strings.Contains(list, "✓ inspect") || strings.Contains(list, "⚡ ✓ inspect") || !strings.Contains(list, "succeeded") {
		t.Fatalf("action list does not expose result:\n%s", list)
	}
	lines := strings.Split(list, "\n")
	serviceColumn, actionColumn := -1, -1
	for _, line := range lines {
		if strings.Contains(line, "app") {
			serviceColumn = lipgloss.Width(line[:strings.Index(line, "●")])
		}
		if strings.Contains(line, "inspect") {
			actionColumn = lipgloss.Width(line[:strings.Index(line, "✓")])
		}
	}
	if serviceColumn < 0 || serviceColumn != actionColumn {
		t.Fatalf("service/action status columns = %d/%d:\n%s", serviceColumn, actionColumn, list)
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
	list := ansi.Strip(model.renderServicePanel(60, 8))
	if !strings.Contains(list, "› ▾   tools") || !strings.Contains(list, "○ version") || strings.Contains(list, "⚡") {
		t.Fatalf("group/action markers are ambiguous:\n%s", list)
	}
	model.moveServiceListCursor(1)
	if model.focusedAction == nil || model.focusedAction.OwnerKind != config.ActionOwnerGroup {
		t.Fatalf("focused group action = %#v", model.focusedAction)
	}
	command, handled := model.toggleFocusedAction()
	if !handled || command == nil {
		t.Fatalf("run group action = handled %v, command %v", handled, command)
	}
	_, _ = model.Update(command())
	_, _, state, exists := model.focusedActionDefinition()
	if !exists || state.Status != service.ActionSucceeded || strings.TrimSpace(strings.Join(state.Stdout, "")) != "v1" {
		t.Fatalf("group action state = %#v, %v", state, exists)
	}
}

func TestExpandedActionsDoNotInsertBlankRows(t *testing.T) {
	model := NewModel(&config.Config{Project: "Rows", Services: map[string]config.Service{
		"app": {Command: "run", Actions: map[string]config.Action{
			"build": {Command: "exit 0"},
			"check": {Command: "exit 0"},
		}},
		"worker": {Command: "run"},
	}}, "test")
	defer model.Shutdown()
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerService, "app")] = true
	model.focusServiceListRow(1)

	plain := ansi.Strip(model.renderServicePanel(60, 10))
	lines := strings.Split(plain, "\n")
	positions := map[string]int{}
	for index, line := range lines {
		for _, label := range []string{"build", "check", "worker"} {
			if strings.Contains(line, label) {
				positions[label] = index
			}
		}
	}
	if positions["check"] != positions["build"]+1 || positions["worker"] != positions["check"]+1 {
		t.Fatalf("expanded actions are not consecutive: positions %v\n%s", positions, plain)
	}
	if !strings.Contains(plain, "›   ○ build") || !strings.Contains(plain, "  □ ● worker") {
		t.Fatalf("single-column selection markers are misaligned:\n%s", plain)
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
	if command, handled := model.toggleFocusedAction(); !handled || command != nil {
		t.Fatalf("confirmation action = handled %v, command %v", handled, command)
	}
	model.focusServiceListRow(2)
	if command, handled := model.toggleFocusedAction(); !handled || command != nil {
		t.Fatalf("interactive action = handled %v, command %v", handled, command)
	}
	for _, id := range model.cfg.ActionIDs() {
		state, _ := model.manager.ActionState(id)
		if state.Status != service.ActionReady {
			t.Fatalf("protected action %s state = %#v", id.Name, state)
		}
	}
}
