package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// parameterTreeModel focuses the one parameterized group action and expands its
// owner, leaving the action row focused and ready for Enter.
func parameterTreeModel(t *testing.T) (*Model, config.ActionID) {
	t.Helper()
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"infra": {Actions: map[string]config.Action{
			"seed": {
				Params: map[string]config.ActionParam{
					"env":     {Type: "radio", Options: config.ActionParamOptions{{Value: "dev"}, {Value: "prod", Confirm: "production is live"}}, Default: "dev", Arg: "--env", Prompt: "Target environment"},
					"verbose": {Type: "checkbox", Default: false, Flag: "--verbose"},
				},
				ParamOrder: []string{"env", "verbose"},
				Argv:       config.ArgvList{"/bin/echo", "{{env}}", "{{verbose}}"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"seed"}},
	}, ActionGroupOrder: []string{"infra"}}, "test")
	t.Cleanup(func() { _ = model.Shutdown() })
	model.width, model.height, model.ready = 120, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "infra", Name: "seed"}
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, "infra")] = true
	for index, row := range model.serviceListRows() {
		if row.Kind == actionRowAction && row.Action == id {
			model.focusServiceListRow(index)
		}
	}
	return model, id
}

func rowKinds(rows []actionListRow) []string {
	kinds := make([]string, 0, len(rows))
	for _, row := range rows {
		switch row.Kind {
		case actionRowParam:
			kinds = append(kinds, "param:"+row.Param)
		case actionRowParamValue:
			kinds = append(kinds, "value:"+row.Param+"="+row.Value)
		case actionRowPreview:
			kinds = append(kinds, "preview:"+row.Preview)
		}
	}
	return kinds
}

func focusRow(t *testing.T, model *Model, want string) {
	t.Helper()
	rows := model.serviceListRows()
	for index, row := range rows {
		for _, kind := range rowKinds([]actionListRow{row}) {
			if kind == want {
				model.focusServiceListRow(index)
				return
			}
		}
	}
	t.Fatalf("no row %q in %#v", want, rowKinds(rows))
}

func TestParameterTreeExpandsInDeclarationOrderWithALivePreview(t *testing.T) {
	model, id := parameterTreeModel(t)

	if rows := model.actionParamRows(id); len(rows) != 0 {
		t.Fatalf("a collapsed action already listed its parameters: %#v", rowKinds(rows))
	}
	if _, handled := model.openFocusedListItem(); !handled {
		t.Fatal("Enter on a parameterized action did nothing")
	}

	kinds := rowKinds(model.actionParamRows(id))
	if len(kinds) != 3 || kinds[1] != "param:env" || kinds[2] != "param:verbose" {
		t.Fatalf("parameter rows = %#v, want declaration order", kinds)
	}
	if kinds[0] != "preview:/bin/echo --env dev" {
		t.Fatalf("preview row = %q", kinds[0])
	}

	// A value row is only listed once its parameter is opened in turn.
	focusRow(t, model, "param:env")
	if _, handled := model.openFocusedListItem(); !handled {
		t.Fatal("Enter on a parameter row did nothing")
	}
	kinds = rowKinds(model.actionParamRows(id))
	if len(kinds) != 5 || kinds[2] != "value:env=dev" || kinds[3] != "value:env=prod" {
		t.Fatalf("opened parameter rows = %#v", kinds)
	}

	focusRow(t, model, "value:env=prod")
	if _, handled := model.openFocusedListItem(); !handled {
		t.Fatal("Enter on a value row did nothing")
	}
	focusRow(t, model, "param:verbose")
	if _, handled := model.openFocusedListItem(); !handled {
		t.Fatal("Enter on a checkbox row did nothing")
	}
	kinds = rowKinds(model.actionParamRows(id))
	if kinds[0] != "preview:/bin/echo --env prod --verbose" {
		t.Fatalf("the preview did not follow the chosen values: %#v", kinds)
	}
	if got := model.paramRowText(id, "verbose"); got != markChecked {
		t.Fatalf("checkbox row reads %q", got)
	}
	if marker := model.paramValueMarker(id, "env", "prod"); marker != markChosen {
		t.Fatalf("selected option marker = %q", marker)
	}
}

func TestDownMovesPastAnExpandedActionCommand(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.cfg.ActionGroups["later"] = config.ActionGroup{Actions: map[string]config.Action{
		"inspect": {Command: "exit 0"},
	}}
	model.cfg.ActionGroupOrder = []string{"infra", "later"}
	model.expandedActionParams[id] = true

	focusRow(t, model, "preview:/bin/echo --env dev")
	previewIndex := model.focusedServiceListRow()
	if previewIndex < 0 || model.serviceListRows()[previewIndex].Kind != actionRowPreview {
		t.Fatalf("command preview is not addressable at row %d", previewIndex)
	}

	model.moveServiceListCursor(1) // first parameter
	model.moveServiceListCursor(1) // second parameter
	model.moveServiceListCursor(1) // following owner
	row := model.serviceListRows()[model.focusedServiceListRow()]
	if row.Kind != actionRowGroup || row.Group != "later" {
		t.Fatalf("Down from command preview focused %#v, want the following owner", row)
	}
}

func TestExpandedActionLabelsParametersAndCommand(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.expandedActionParams[id] = true

	plain := ansi.Strip(model.renderServicePanel(80, 12))
	for _, want := range []string{
		"env:     dev",
		"verbose: " + markUnchecked,
		"$ /bin/echo --env dev",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expanded action does not distinguish %q:\n%s", want, plain)
		}
	}
	for _, unwanted := range []string{"PARAMS", "COMMAND", "PARAM "} {
		if strings.Contains(plain, unwanted) {
			t.Fatalf("expanded action still contains %q:\n%s", unwanted, plain)
		}
	}
	// The owner's own disclosure triangle stays; the action's does not, because
	// opening an action offers settings rather than a nested list.
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "seed") && (strings.Contains(line, "▾") || strings.Contains(line, "▸")) {
			t.Fatalf("the action row still carries a disclosure triangle: %q", line)
		}
	}

	// Collapsed, the action still has to advertise that its command is
	// configurable, and the ellipsis is that affordance.
	model.expandedActionParams[id] = false
	collapsed := ansi.Strip(model.renderServicePanel(80, 12))
	if !strings.Contains(collapsed, "seed ◆") {
		t.Fatalf("a collapsed parameterized action does not offer its parameters:\n%s", collapsed)
	}
}

func TestParameterMarkSurvivesRunMetadataInNarrowList(t *testing.T) {
	for _, status := range []app.ActionStatus{app.ActionRunning, app.ActionSucceeded, app.ActionFailed} {
		t.Run(status.String(), func(t *testing.T) {
			model, id := parameterTreeModel(t)
			model.actionStates[id] = app.ActionResult{ID: id, Run: 1, Status: status, Duration: time.Second}
			row := model.serviceListRows()[model.focusedServiceListRow()]
			for _, width := range []int{17, 80} {
				line := ansi.Strip(model.renderServiceListRow(model.focusedServiceListRow(), row, width))
				if !strings.Contains(line, "seed#1 ◆") {
					t.Fatalf("width %d hides the parameter mark after a run: %q", width, line)
				}
				if width == 80 && !strings.Contains(line, "◆  "+status.String()) {
					t.Fatalf("run status must follow the parameter mark: %q", line)
				}
			}
		})
	}
}

func TestParameterTreeRendersInsideANarrowViewport(t *testing.T) {
	model, id := parameterTreeModel(t)
	if _, handled := model.openFocusedListItem(); !handled {
		t.Fatal("Enter on a parameterized action did nothing")
	}
	focusRow(t, model, "param:env")
	if _, handled := model.openFocusedListItem(); !handled {
		t.Fatal("Enter on a parameter row did nothing")
	}
	model.width, model.height = 70, 16
	model.refreshServices()
	view := ansi.Strip(model.View())
	for _, fragment := range []string{"seed", "env", "verbose"} {
		if !strings.Contains(view, fragment) {
			t.Fatalf("narrow viewport dropped %q:\n%s", fragment, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if width := ansi.StringWidth(line); width > model.width {
			t.Fatalf("line wider than the viewport (%d > %d): %q", width, model.width, line)
		}
	}
	_ = id
}

func TestAnIncompleteParameterFormBlocksTheRun(t *testing.T) {
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"infra": {Actions: map[string]config.Action{
			"seed": {
				Params:     map[string]config.ActionParam{"target": {Type: "text", Required: true, Arg: "--target"}},
				ParamOrder: []string{"target"},
				Argv:       config.ArgvList{"/bin/echo", "{{target}}"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"seed"}},
	}, ActionGroupOrder: []string{"infra"}}, "test")
	defer func() { _ = model.Shutdown() }()
	model.width, model.height, model.ready = 120, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "infra", Name: "seed"}
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, "infra")] = true
	model.expandedActionParams[id] = true

	// A required value nobody filled in has no preview to show and no run to
	// start: the form reports the field instead of guessing.
	// The command row stays, empty, so the tree says "this cannot run yet"
	// instead of quietly losing a line.
	// The row keeps the template of the command the values cannot build yet.
	if kinds := rowKinds(model.actionParamRows(id)); len(kinds) != 2 || kinds[0] != "preview:/bin/echo {{target}}" || kinds[1] != "param:target" {
		t.Fatalf("an invalid form previewed %#v", kinds)
	}
	// The row keeps showing what the command would be; what is wrong with it
	// is said in the details panel, not twice in the list.
	rendered := ansi.Strip(model.renderServicePanel(80, 12))
	if !strings.Contains(rendered, "$ /bin/echo") {
		t.Fatalf("the blocked command row lost its template:\n%s", rendered)
	}
	for index, row := range model.serviceListRows() {
		if row.Kind == actionRowAction && row.Action == id {
			model.focusServiceListRow(index)
		}
	}
	details := ansi.Strip(model.renderActionDetails(74, 18))
	if !strings.Contains(details, "PROBLEM") || !strings.Contains(details, "target") {
		t.Fatalf("the details do not name what is missing:\n%s", details)
	}
	if reason := model.paramRowError(id, "target"); reason == "" {
		t.Fatal("the missing required value is not reported on its row")
	}
	before := len(model.notifications)
	if command := model.runParameterizedAction(id); command != nil {
		t.Fatal("an incomplete form started a run")
	}
	if len(model.notifications) <= before {
		t.Fatal("the refused run said nothing")
	}
}

func TestParameterValuesSurviveAKeyThatIsNotForThem(t *testing.T) {
	model, id := parameterTreeModel(t)
	if _, handled := model.openFocusedListItem(); !handled {
		t.Fatal("Enter on a parameterized action did nothing")
	}
	focusRow(t, model, "param:verbose")
	if _, handled := model.openFocusedListItem(); !handled {
		t.Fatal("Enter on a checkbox row did nothing")
	}
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if got := model.paramRowText(id, "verbose"); got != markChecked {
		t.Fatalf("navigation reset a chosen value: %q", got)
	}
}

func TestATextParameterIsTypedInPlace(t *testing.T) {
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"infra": {Actions: map[string]config.Action{
			"seed": {
				Params:     map[string]config.ActionParam{"target": {Type: "text", Required: true, Arg: "--target"}},
				ParamOrder: []string{"target"},
				Argv:       config.ArgvList{"/bin/echo", "{{target}}"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"seed"}},
	}, ActionGroupOrder: []string{"infra"}}, "test")
	defer func() { _ = model.Shutdown() }()
	model.width, model.height, model.ready = 120, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "infra", Name: "seed"}
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, "infra")] = true
	model.expandedActionParams[id] = true
	focusRow(t, model, "param:target")

	if _, handled := model.openFocusedListItem(); !handled || model.mode != ModeParamEdit {
		t.Fatalf("Enter on a text row did not open the editor: handled=%v mode=%v", handled, model.mode)
	}
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("phone")},
		{Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune("x")},
		{Type: tea.KeyBackspace},
	} {
		model.handleKeyMsg(key)
	}
	if !model.paramEditing(id, "target") || model.paramInput.Value() != "phone " {
		t.Fatalf("editor holds %q, editing = %v", model.paramInput.Value(), model.paramEditing(id, "target"))
	}

	// Esc restores the row rather than keeping a half-typed value.
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEsc})
	if model.mode != ModeNormal || ansi.Strip(model.paramRowText(id, "target")) != "______" {
		t.Fatalf("Esc kept the draft: mode=%v value=%q", model.mode, model.paramRowText(id, "target"))
	}

	model.openFocusedListItem()
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("phone")})
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if model.mode != ModeNormal {
		t.Fatalf("Enter did not close the editor: mode=%v", model.mode)
	}
	kinds := rowKinds(model.actionParamRows(id))
	if kinds[0] != "preview:/bin/echo --target phone" {
		t.Fatalf("the typed value did not reach the preview: %#v", kinds)
	}
	if reason := model.paramRowError(id, "target"); reason != "" {
		t.Fatalf("a filled required value still reports %q", reason)
	}
}

func TestAnEmptyStringTypedOnPurposeIsAValue(t *testing.T) {
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"infra": {Actions: map[string]config.Action{
			"say": {
				Params:     map[string]config.ActionParam{"message": {Type: "text", Optional: true, Arg: "--message"}},
				ParamOrder: []string{"message"},
				Argv:       config.ArgvList{"/bin/echo", "{{message}}"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"say"}},
	}, ActionGroupOrder: []string{"infra"}}, "test")
	defer func() { _ = model.Shutdown() }()
	model.width, model.height, model.ready = 120, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "infra", Name: "say"}
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, "infra")] = true
	model.expandedActionParams[id] = true

	kinds := rowKinds(model.actionParamRows(id))
	if kinds[0] != "preview:/bin/echo" {
		t.Fatalf("an untouched optional value contributed an argument: %#v", kinds)
	}

	focusRow(t, model, "param:message")
	model.openFocusedListItem()
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	kinds = rowKinds(model.actionParamRows(id))
	if kinds[0] != "preview:/bin/echo --message ''" {
		t.Fatalf("an empty string typed on purpose was dropped: %#v", kinds)
	}
}

func TestDownWalksThroughEveryParameterValue(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.expandedActionParams[id] = true
	model.expandedParamOptions[actionParamKey(id, "env")] = true

	focusRow(t, model, "value:env=dev")
	visited := []string{}
	for step := 0; step < 6; step++ {
		model.moveServiceListCursor(1)
		row := model.serviceListRows()[model.focusedServiceListRow()]
		switch row.Kind {
		case actionRowParamValue:
			visited = append(visited, "value:"+row.Value)
		case actionRowParam:
			visited = append(visited, "param:"+row.Param)
		default:
			visited = append(visited, "other")
		}
	}
	// Without an exact focus match the cursor reads its own position as the
	// parameter row above, and every Down lands back on the first value.
	if len(visited) < 2 || visited[0] != "value:prod" || visited[1] != "param:verbose" {
		t.Fatalf("Down from the first value walked %#v", visited)
	}
}

func TestDetailsDescribeTheCompiledCommandAndTheFocusedParameter(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.expandedActionParams[id] = true

	action, _ := model.cfg.ResolveAction(id)
	state, _ := model.app.ActionState(id)

	onAction := strings.Join(model.actionDetailLines(id, action, state, 70), "\n")
	if !strings.Contains(onAction, "/bin/echo --env dev") {
		t.Fatalf("details show no compiled command:\n%s", onAction)
	}
	if strings.Contains(onAction, "COMMAND —") {
		t.Fatalf("details still show a dash for a parameterized action:\n%s", onAction)
	}
	if strings.Contains(onAction, "PARAMETER") {
		t.Fatalf("details describe a parameter while the action row is focused:\n%s", onAction)
	}

	focusRow(t, model, "param:env")
	onParam := ansi.Strip(model.renderActionDetails(74, 18))
	for _, want := range []string{"[2] PARAMETER", "seed → env", "Target environment", "OPTIONS", markChosen + " dev", markUnchosen + " prod", "production is live"} {
		if !strings.Contains(onParam, want) {
			t.Fatalf("focused parameter details omit %q:\n%s", want, onParam)
		}
	}
	// Standing on a parameter, the panel answers about that parameter only.
	for _, unwanted := range []string{"DIRECTORY", "MODE", "COMMAND"} {
		if strings.Contains(onParam, unwanted) {
			t.Fatalf("focused parameter details still describe the action (%q):\n%s", unwanted, onParam)
		}
	}

	// Choosing the value that asks must be visible before the modal appears.
	model.expandedParamOptions[actionParamKey(id, "env")] = true
	focusRow(t, model, "value:env=prod")
	model.openFocusedListItem()
	model.focusedParam = nil
	asked := ansi.Strip(strings.Join(model.actionDetailLines(id, action, state, 70), "\n"))
	if !strings.Contains(asked, "confirmation required") || !strings.Contains(asked, "--env prod") {
		t.Fatalf("details do not follow the chosen value:\n%s", asked)
	}
}

func TestACheckboxGroupTakesSeveralValuesWithCheckMarks(t *testing.T) {
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"native": {Actions: map[string]config.Action{
			"install": {
				Params: map[string]config.ActionParam{"platform": {
					Type:    "checkbox",
					Options: config.ActionParamOptions{{Value: "phone", Label: "Phone", Flag: "--phone"}, {Value: "watch", Label: "Watch", Flag: "--watch"}},
					Default: []any{"phone"},
					Prompt:  "Target platform",
				}},
				ParamOrder: []string{"platform"},
				Run:        config.ArgvList{"./install.sh"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"install"}},
	}, ActionGroupOrder: []string{"native"}}, "test")
	defer func() { _ = model.Shutdown() }()
	model.width, model.height, model.ready = 120, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "native", Name: "install"}
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, "native")] = true
	model.expandedActionParams[id] = true
	model.expandedParamOptions[actionParamKey(id, "platform")] = true

	// A group of values is a set, not a choice: its rows are boxes, and
	// picking a second one must not release the first.
	if marker := model.paramValueMarker(id, "platform", "phone"); marker != markChecked {
		t.Fatalf("selected group value marker = %q, want a check box", marker)
	}
	if marker := model.paramValueMarker(id, "platform", "watch"); marker != markUnchecked {
		t.Fatalf("unselected group value marker = %q", marker)
	}

	focusRow(t, model, "value:platform=watch")
	model.openFocusedListItem()
	kinds := rowKinds(model.actionParamRows(id))
	if kinds[0] != "preview:./install.sh --phone --watch" {
		t.Fatalf("a second value replaced the first instead of joining it: %#v", kinds)
	}
	if model.paramValueMarker(id, "platform", "phone") != markChecked || model.paramValueMarker(id, "platform", "watch") != markChecked {
		t.Fatal("both chosen values must read as chosen")
	}

	focusRow(t, model, "value:platform=phone")
	model.openFocusedListItem()
	kinds = rowKinds(model.actionParamRows(id))
	if kinds[0] != "preview:./install.sh --watch" {
		t.Fatalf("unchecking a value did not remove its flag: %#v", kinds)
	}
}

func TestSpaceMarksValuesAndLeavesTypingAlone(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.expandedActionParams[id] = true
	model.expandedParamOptions[actionParamKey(id, "env")] = true
	space := tea.KeyMsg{Type: tea.KeySpace}

	focusRow(t, model, "value:env=prod")
	model.handleKeyMsg(space)
	if got := model.paramRowText(id, "env"); got != "prod" {
		t.Fatalf("Space on a value did not pick it: %q", got)
	}

	focusRow(t, model, "param:verbose")
	model.handleKeyMsg(space)
	if got := model.paramRowText(id, "verbose"); got != markChecked {
		t.Fatalf("Space on a checkbox did not toggle it: %q", got)
	}
	model.handleKeyMsg(space)
	if got := model.paramRowText(id, "verbose"); got != markUnchecked {
		t.Fatalf("Space did not toggle the checkbox back: %q", got)
	}

	// Inside the in-place editor Space is a space, not a marker.
	focusRow(t, model, "param:env")
	model.handleKeyMsg(space)
	if model.mode != ModeNormal {
		t.Fatalf("Space opened an editor: mode = %v", model.mode)
	}
}

func alignmentModel(t *testing.T, names ...string) (*Model, config.ActionID) {
	t.Helper()
	params := map[string]config.ActionParam{}
	for _, name := range names {
		params[name] = config.ActionParam{Type: "text", Optional: true, Arg: "--" + name}
	}
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"g": {Actions: map[string]config.Action{
			"a": {Params: params, ParamOrder: names, Run: config.ArgvList{"./a.sh"}, Dir: t.TempDir()},
		}, ActionOrder: []string{"a"}},
	}, ActionGroupOrder: []string{"g"}}, "test")
	t.Cleanup(func() { _ = model.Shutdown() })
	model.width, model.height, model.ready = 120, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "g", Name: "a"}
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, "g")] = true
	model.expandedActionParams[id] = true
	return model, id
}

func TestLabelsShareAColumnOnlyWhileTheNamesAreClose(t *testing.T) {
	// Names within a few characters of each other read better in one column.
	near, id := alignmentModel(t, "env", "count", "label")
	// Each label is padded to the longest name plus its colon; the row adds the
	// single space that separates it from the value.
	for name, want := range map[string]string{"env": "env:  ", "count": "count:", "label": "label:"} {
		if got := ansi.Strip(near.paramRowLabel(id, name)); got != want {
			t.Fatalf("aligned label %q = %q, want %q", name, got, want)
		}
	}

	// One long name would push every value away from its own label, so the
	// column is dropped and each value follows its name directly.
	far, farID := alignmentModel(t, "env", "personalization_engine_target")
	for _, name := range []string{"env", "personalization_engine_target"} {
		if got := ansi.Strip(far.paramRowLabel(farID, name)); got != name+":" {
			t.Fatalf("unaligned label %q = %q", name, got)
		}
	}
}

func TestABlockedCommandNamesTheValueThatBlocksIt(t *testing.T) {
	model, id := alignmentModel(t, "target")
	specs := model.actionParamSpecs(id)
	if len(specs) != 1 {
		t.Fatalf("specs = %#v", specs)
	}
	// An optional parameter never blocks, so make this one required by hand.
	action := model.cfg.ActionGroups["g"].Actions["a"]
	param := action.Params["target"]
	param.Optional, param.Required = false, true
	action.Params["target"] = param
	model.cfg.ActionGroups["g"].Actions["a"] = action
	model.actionParamValues = map[config.ActionID]map[string]paramUIValue{}

	if !model.paramRowBlocked(id, "target") {
		t.Fatal("a missing required value does not block the command")
	}
	rows := model.actionParamRows(id)
	if rows[0].Kind != actionRowPreview || !rows[0].Blocked {
		t.Fatalf("a blocked action did not mark its command row: %#v", rows[0])
	}
	if problem := model.paramCommandProblem(id); !strings.Contains(problem, "target") {
		t.Fatalf("the problem does not name the parameter: %q", problem)
	}
}

func TestRunBelongsToTheActionAndItsCommandOnly(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.expandedActionParams[id] = true

	focusRow(t, model, "param:env")
	if !model.paramRowFocused() {
		t.Fatal("a parameter row is not recognized as one")
	}
	for _, button := range model.actionButtons() {
		if strings.Contains(ansi.Strip(button.rendered), "Run action") {
			t.Fatalf("the action bar offers a run from a parameter row: %q", ansi.Strip(button.rendered))
		}
	}
	_, command, handled := model.handleLifecycleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if command != nil {
		t.Fatal("s started the action from a parameter row")
	}
	_ = handled

	focusRow(t, model, "preview:/bin/echo --env dev")
	if model.paramRowFocused() {
		t.Fatal("the command row is treated as a parameter row")
	}
	found := false
	for _, button := range model.actionButtons() {
		if strings.Contains(ansi.Strip(button.rendered), "Run action") {
			found = true
		}
	}
	if !found {
		t.Fatal("the command row does not offer to run what it shows")
	}
}
