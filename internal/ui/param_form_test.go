package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/muesli/termenv"
)

func TestCOpensTheFormFromTheActionAndFromASetting(t *testing.T) {
	model, id := parameterTreeModel(t)
	c := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}

	model.handleKeyMsg(c)
	if model.mode != ModeParamForm || model.paramForm == nil || model.paramForm.ID != id {
		t.Fatalf("c on the action did not open its form: mode=%v", model.mode)
	}
	model.closeParamForm()

	model.expandedActionParams[id] = true
	focusRow(t, model, "param:verbose")
	model.handleKeyMsg(c)
	if model.mode != ModeParamForm {
		t.Fatalf("c on a setting did not open the form: mode=%v", model.mode)
	}
}

func TestTheFormShowsTheCommandAboveItsFields(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.openParamForm(id)
	view := ansi.Strip(model.renderParamFormView())

	commandLine, envLine := -1, -1
	for index, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "$ /bin/echo --env dev") {
			commandLine = index
		}
		if strings.Contains(line, "env") && strings.Contains(line, markChosen+" dev") {
			envLine = index
		}
	}
	if commandLine < 0 || envLine < 0 {
		t.Fatalf("form is missing the command or its fields:\n%s", view)
	}
	if commandLine > envLine {
		t.Fatalf("the command must stay above the fields that build it:\n%s", view)
	}
	for _, want := range []string{"Configure infra/seed", "[s] Run", "[Esc] Close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("form omits %q:\n%s", want, view)
		}
	}
}

func TestTheFormEditsValuesAndTheCommandFollows(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.openParamForm(id)

	// Moving across a single-choice field selects as it goes.
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRight})
	if got := model.paramRowText(id, "env"); got != "prod" {
		t.Fatalf("right did not choose the next value: %q", got)
	}
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.paramRowText(id, "env"); got != "dev" {
		t.Fatalf("left did not go back: %q", got)
	}

	// Space checks a box on the next field.
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyDown})
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeySpace})
	if got := model.paramRowText(id, "verbose"); got != markChecked {
		t.Fatalf("space did not check the box: %q", got)
	}

	view := ansi.Strip(model.renderParamFormView())
	if !strings.Contains(view, "$ /bin/echo --env dev --verbose") {
		t.Fatalf("the command did not follow the values:\n%s", view)
	}
}

func TestFormShowsEveryChoiceWhenOptionsExceedTheRow(t *testing.T) {
	for _, control := range []string{"radio", "checkbox"} {
		t.Run(control, func(t *testing.T) {
			model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
				"g": {Actions: map[string]config.Action{
					"a": {
						Params: map[string]config.ActionParam{"target": {
							Type:     control,
							Optional: true,
							Options: config.ActionParamOptions{
								{Value: "first", Label: "First environment"},
								{Value: "second", Label: "Second environment with a longer description"},
								{Value: "third", Label: "Third environment"},
							},
						}},
						ParamOrder: []string{"target"},
						Run:        config.ArgvList{"/bin/echo"},
						Dir:        t.TempDir(),
					},
				}, ActionOrder: []string{"a"}},
			}, ActionGroupOrder: []string{"g"}}, "test")
			t.Cleanup(func() { _ = model.Shutdown() })
			model.width, model.height, model.ready = 72, 40, true
			id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "g", Name: "a"}
			model.openParamForm(id)

			view := ansi.Strip(model.renderParamFormView())
			for _, label := range []string{"First environment", "Second environment with a longer description", "Third environment"} {
				if !strings.Contains(strings.ReplaceAll(view, "\n", ""), label) {
					t.Fatalf("form lost choice %q:\n%s", label, view)
				}
			}
		})
	}
}

func TestTheFormTypesIntoATextFieldAndRuns(t *testing.T) {
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"g": {Actions: map[string]config.Action{
			"say": {
				Params:     map[string]config.ActionParam{"message": {Type: "text", Required: true, Arg: "--message"}},
				ParamOrder: []string{"message"},
				Run:        config.ArgvList{"/bin/echo"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"say"}},
	}, ActionGroupOrder: []string{"g"}}, "test")
	defer func() { _ = model.Shutdown() }()
	model.width, model.height, model.ready = 120, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "g", Name: "say"}
	model.openParamForm(id)

	// A required value nobody has typed blocks the command, and the form says
	// so where the command would be.
	blocked := ansi.Strip(model.renderParamFormView())
	if !strings.Contains(blocked, "a value is required") {
		t.Fatalf("the form hides why it cannot run:\n%s", blocked)
	}

	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if model.paramForm == nil || !model.paramForm.Editing {
		t.Fatal("Enter did not start editing the text field")
	}
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeySpace})
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("there")})
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if model.paramForm.Editing {
		t.Fatal("Enter did not keep the typed value")
	}
	if got := model.paramRowText(id, "message"); got != "hello there" {
		t.Fatalf("typed value = %q", got)
	}
	view := ansi.Strip(model.renderParamFormView())
	if !strings.Contains(view, "$ /bin/echo --message 'hello there'") {
		t.Fatalf("the command did not take the typed value:\n%s", view)
	}

	// s closes the form and starts what the form showed.
	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if command == nil || model.mode != ModeNormal || model.paramForm != nil {
		t.Fatalf("s did not run and close: command=%v mode=%v", command != nil, model.mode)
	}
}

func TestAParameterRowOffersOnlyTheForm(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.expandedActionParams[id] = true
	focusRow(t, model, "param:env")

	labels := []string{}
	for _, button := range model.actionButtons() {
		labels = append(labels, ansi.Strip(button.rendered))
	}
	joined := strings.Join(labels, " | ")
	for _, unwanted := range []string{"Start", "Force", "Run action", "Restart"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("a parameter row offers %q: %s", unwanted, joined)
		}
	}
	if !strings.Contains(joined, "Form: c") {
		t.Fatalf("a parameter row does not offer the form: %s", joined)
	}

	// Force start is as silent as start there.
	_, command, _ := model.handleLifecycleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if command != nil {
		t.Fatal("force start ran from a parameter row")
	}
}

func TestTheFormKeepsItsGeometry(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.width, model.height = 96, 30
	model.openParamForm(id)

	measure := func() (int, int) {
		lines := strings.Split(ansi.Strip(model.renderParamFormView()), "\n")
		widest, left := 0, -1
		for _, line := range lines {
			if index := strings.Index(line, "Configure"); index >= 0 && left < 0 {
				left = index
			}
			if width := len([]rune(line)); width > widest {
				widest = width
			}
		}
		return left, widest
	}

	// Typing into a field, or moving to one whose hint is longer, must not
	// move the frame under the hands using it.
	left, widest := measure()
	states := []func(){
		func() { model.moveParamFormField(1) },
		func() { model.handleParamFormKeys(tea.KeyMsg{Type: tea.KeyEnter}) },
		func() { model.paramInput.SetValue("a much longer value than the field held before") },
		func() { model.commitParamFormEdit(false) },
		func() { model.moveParamFormField(-1) },
	}
	for index, step := range states {
		step()
		if nextLeft, nextWidest := measure(); nextLeft != left || nextWidest != widest {
			t.Fatalf("step %d resized the form: left %d→%d, width %d→%d", index, left, nextLeft, widest, nextWidest)
		}
	}
}

func TestAFieldShowsItsOwnError(t *testing.T) {
	model, id := alignmentModel(t, "target")
	action := model.cfg.ActionGroups["g"].Actions["a"]
	param := action.Params["target"]
	param.Optional, param.Required = false, true
	action.Params["target"] = param
	model.cfg.ActionGroups["g"].Actions["a"] = action
	model.openParamForm(id)

	view := ansi.Strip(model.renderParamFormView())
	lines := strings.Split(view, "\n")
	field, problem := -1, -1
	for index, line := range lines {
		if strings.Contains(line, "target") {
			field = index
		}
		if strings.Contains(line, "a value is required") {
			problem = index
		}
	}
	if field < 0 || problem < 0 {
		t.Fatalf("the form does not show the field and its problem:\n%s", view)
	}
	if problem != field+1 {
		t.Fatalf("the problem is not under its own field:\n%s", view)
	}
}

func TestTheFormSaysWhatAFieldAcceptsAndGroupsItsKeys(t *testing.T) {
	minimum, maximum := int64(1), int64(10)
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"g": {Actions: map[string]config.Action{
			"a": {
				Params: map[string]config.ActionParam{"count": {
					Type: "number", Default: 3, Min: &minimum, Max: &maximum, Arg: "--count", Prompt: "How many",
				}},
				ParamOrder: []string{"count"},
				Run:        config.ArgvList{"./a.sh"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"a"}},
	}, ActionGroupOrder: []string{"g"}}, "test")
	defer func() { _ = model.Shutdown() }()
	model.width, model.height, model.ready = 110, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "g", Name: "a"}
	model.openParamForm(id)

	view := ansi.Strip(model.renderParamFormView())
	// The limits belong where the value is typed, not only in the reference.
	if !strings.Contains(view, "How many · from 1 to 10") {
		t.Fatalf("the field does not say what it accepts:\n%s", view)
	}
	for _, group := range []string{"MOVE", "SET", "RUN"} {
		if !strings.Contains(view, group) {
			t.Fatalf("the footer is not grouped (%q missing):\n%s", group, view)
		}
	}
	if !strings.Contains(view, "[↑/k] Up") || !strings.Contains(view, "[←/h] [→/l] Value") {
		t.Fatalf("the footer hides the letter keys:\n%s", view)
	}

	// Out of range, the same line turns into the reason.
	values := model.paramValuesFor(id)
	value := values["count"]
	value.Text, value.Set = "99", true
	values["count"] = value
	model.actionParamValues[id] = values
	blocked := ansi.Strip(model.renderParamFormView())
	if !strings.Contains(blocked, "expected an integer between 1 and 10") {
		t.Fatalf("the field does not say what is wrong:\n%s", blocked)
	}
}

func TestAnEnvironmentValueChangesTheCommandInTheForm(t *testing.T) {
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"g": {Actions: map[string]config.Action{
			"seed": {
				Params:     map[string]config.ActionParam{"write": {Type: "checkbox", Default: false, Env: "WRITE"}},
				ParamOrder: []string{"write"},
				Run:        config.ArgvList{"./seed.sh"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"seed"}},
	}, ActionGroupOrder: []string{"g"}}, "test")
	defer func() { _ = model.Shutdown() }()
	model.width, model.height, model.ready = 110, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "g", Name: "seed"}
	model.openParamForm(id)

	before := ansi.Strip(model.renderParamFormView())
	if !strings.Contains(before, "WRITE=0 ./seed.sh") {
		t.Fatalf("an environment value is invisible in the command:\n%s", before)
	}
	model.toggleParamFormValue()
	after := ansi.Strip(model.renderParamFormView())
	if !strings.Contains(after, "WRITE=1 ./seed.sh") {
		t.Fatalf("toggling an environment value changed nothing:\n%s", after)
	}
}

func TestTheFormKeepsItsHeightWhileTyping(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.width, model.height = 96, 30
	model.openParamForm(id)
	height := func() int { return len(strings.Split(ansi.Strip(model.renderParamFormView()), "\n")) }

	before := height()
	model.moveParamFormField(1)
	model.handleParamFormKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if during := height(); during != before {
		t.Fatalf("the form changed height on entering a field: %d → %d", before, during)
	}
	model.commitParamFormEdit(false)
	if after := height(); after != before {
		t.Fatalf("the form changed height on leaving a field: %d → %d", before, after)
	}
}

func TestValuesStepWithLeftAndRight(t *testing.T) {
	minimum, maximum := int64(0), int64(500)
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"g": {Actions: map[string]config.Action{
			"a": {
				Params: map[string]config.ActionParam{
					"env":   {Type: "radio", Options: config.ActionParamOptions{{Value: "dev"}, {Value: "stage"}, {Value: "prod"}}, Default: "dev", Arg: "--env"},
					"count": {Type: "number", Default: 100, Min: &minimum, Max: &maximum, Arg: "--count"},
					"label": {Type: "text", Optional: true, Arg: "--label"},
				},
				ParamOrder: []string{"env", "count", "label"},
				Run:        config.ArgvList{"./a.sh"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"a"}},
	}, ActionGroupOrder: []string{"g"}}, "test")
	defer func() { _ = model.Shutdown() }()
	model.width, model.height, model.ready = 110, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "g", Name: "a"}
	model.openParamForm(id)

	model.handleParamFormKeys(tea.KeyMsg{Type: tea.KeyRight})
	if got := model.paramRowText(id, "env"); got != "stage" {
		t.Fatalf("a list did not step: %q", got)
	}

	// A number counts, and holding shift counts by ten.
	model.moveParamFormField(1)
	model.handleParamFormKeys(tea.KeyMsg{Type: tea.KeyRight})
	if got := model.paramRowText(id, "count"); got != "101" {
		t.Fatalf("a number did not count up: %q", got)
	}
	model.handleParamFormKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	if got := model.paramRowText(id, "count"); got != "111" {
		t.Fatalf("shift did not count by ten: %q", got)
	}
	model.handleParamFormKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	model.handleParamFormKeys(tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.paramRowText(id, "count"); got != "100" {
		t.Fatalf("counting back landed on %q", got)
	}

	// A text field has nothing to step through and stays as it is.
	model.moveParamFormField(1)
	model.handleParamFormKeys(tea.KeyMsg{Type: tea.KeyRight})
	if got := ansi.Strip(model.paramRowText(id, "label")); got != "______" {
		t.Fatalf("a text field changed under the arrows: %q", got)
	}
}

func TestAParameterCanBeSwitchedOff(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.expandedActionParams[id] = true
	focusRow(t, model, "param:verbose")

	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if got := ansi.Strip(model.paramRowText(id, "verbose")); got != disabledParamLabel {
		t.Fatalf("d did not switch the parameter off: %q", got)
	}
	// Switched off, it contributes nothing rather than its default.
	kinds := rowKinds(model.actionParamRows(id))
	if kinds[0] != "preview:/bin/echo --env dev" {
		t.Fatalf("a disabled parameter still reached the command: %#v", kinds)
	}
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if got := ansi.Strip(model.paramRowText(id, "verbose")); got != markUnchecked {
		t.Fatalf("d did not switch it back on: %q", got)
	}

	// The form switches the same parameter off and says so.
	model.openParamForm(id)
	model.moveParamFormField(1)
	model.handleParamFormKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	view := ansi.Strip(model.renderParamFormView())
	if !strings.Contains(view, disabledParamLabel) {
		t.Fatalf("the form does not say the parameter is off:\n%s", view)
	}
}

func TestArrowsStayInsideTheParameterTree(t *testing.T) {
	model, id := parameterTreeModel(t)
	model.expandedActionParams[id] = true
	focusRow(t, model, "param:verbose")

	// A checkbox has nothing to step through, and that is not a reason to
	// leave for the tag list.
	model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRight})
	if model.listMode != listServices {
		t.Fatal("right left the services list from a parameter row")
	}
	if !model.paramRowFocused() || model.focusedParam.Name != "verbose" {
		t.Fatalf("the cursor moved off the parameter: %#v", model.focusedParam)
	}

	// Leaving the list for real drops the parameter focus, so the cursor and
	// the keys cannot point at different things.
	model.focusServiceListRow(0)
	model.toggleListMode()
	if model.focusedParam != nil {
		t.Fatal("the parameter focus survived the list switch")
	}
	model.toggleListMode()
	if model.paramRowFocused() {
		t.Fatal("coming back restored a stale parameter focus")
	}
}

func TestALongCommandIsContinuedAcrossRows(t *testing.T) {
	long := strings.Repeat("value-", 12)
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"g": {Actions: map[string]config.Action{
			"a": {
				Params:     map[string]config.ActionParam{"target": {Type: "text", Default: long, Arg: "--target"}},
				ParamOrder: []string{"target"},
				Run:        config.ArgvList{"./a.sh", "--with", "a-long-fixed-argument"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"a"}},
	}, ActionGroupOrder: []string{"g"}}, "test")
	defer func() { _ = model.Shutdown() }()
	model.width, model.height, model.ready = 100, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "g", Name: "a"}
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, "g")] = true
	model.expandedActionParams[id] = true

	previews := []string{}
	for _, row := range model.actionParamRows(id) {
		if row.Kind == actionRowPreview {
			previews = append(previews, row.Preview)
		}
	}
	if len(previews) < 2 {
		t.Fatalf("a command too long for the list was not continued: %#v", previews)
	}
	for _, part := range previews[:len(previews)-1] {
		if !strings.HasSuffix(part, `\`) {
			t.Fatalf("a continued line does not end in a backslash: %q", part)
		}
	}
	if strings.HasSuffix(previews[len(previews)-1], `\`) {
		t.Fatalf("the last line continues into nothing: %q", previews[len(previews)-1])
	}
	// Nothing is inserted and nothing is dropped: put the lines back together
	// and the command is there again, character for character.
	render, err := model.renderActionParams(id)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := ""
	for _, part := range previews {
		rebuilt += strings.TrimSuffix(part, ` \`)
	}
	if strings.ReplaceAll(rebuilt, " ", "") != strings.ReplaceAll(render.Preview, " ", "") {
		t.Fatalf("the continued command is not the command:\n%q\n%q", rebuilt, render.Preview)
	}
}

func TestTheFooterNeverTrailsOffInAnEllipsis(t *testing.T) {
	for _, width := range []int{44, 52, 66} {
		for _, editing := range []bool{false, true} {
			lines := paramFormFooter(editing, width)
			for _, line := range lines {
				plain := ansi.Strip(line)
				if strings.Contains(plain, "…") {
					t.Fatalf("width %d: a shortcut was cut off: %q", width, plain)
				}
				if got := len([]rune(plain)); got != width {
					t.Fatalf("width %d: footer line is %d wide: %q", width, got, plain)
				}
			}
		}
		// Both states reserve the same room, so the form cannot resize when it
		// starts or stops taking text.
		if len(paramFormFooter(false, width)) != len(paramFormFooter(true, width)) {
			t.Fatalf("width %d: the two footers differ in height", width)
		}
	}
}

func TestDownWalksPastAContinuedCommand(t *testing.T) {
	long := strings.Repeat("value-", 12)
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"g": {Actions: map[string]config.Action{
			"a": {
				Params:     map[string]config.ActionParam{"target": {Type: "text", Default: long, Arg: "--target"}},
				ParamOrder: []string{"target"},
				Run:        config.ArgvList{"./a.sh", "--with", "a-long-fixed-argument"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"a"}},
		"later": {Actions: map[string]config.Action{"inspect": {Command: "exit 0"}}, ActionOrder: []string{"inspect"}},
	}, ActionGroupOrder: []string{"g", "later"}}, "test")
	defer func() { _ = model.Shutdown() }()
	model.width, model.height, model.ready = 100, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "g", Name: "a"}
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, "g")] = true
	model.expandedActionParams[id] = true

	rows := model.serviceListRows()
	continued := 0
	for _, row := range rows {
		if row.Kind == actionRowPreview && row.Continued {
			continued++
		}
	}
	if continued == 0 {
		t.Fatal("this command was expected to need continuing")
	}

	focusRow(t, model, "preview:"+rows[firstPreviewIndex(rows)].Preview)
	// The whole command is one stop: Down from it lands on the parameter, not
	// inside the continuation, and the cursor keeps moving afterwards.
	model.moveServiceListCursor(1)
	if row := rows[0]; row.Kind == actionRowPreview {
		_ = row
	}
	focused := model.serviceListRows()[model.focusedServiceListRow()]
	if focused.Kind != actionRowParam {
		t.Fatalf("Down from a continued command landed on %#v", focused)
	}
	model.moveServiceListCursor(1)
	after := model.serviceListRows()[model.focusedServiceListRow()]
	if after.Kind != actionRowGroup || after.Group != "later" {
		t.Fatalf("the cursor could not leave the action: %#v", after)
	}

	// And back up again, without getting stuck between the command's lines.
	model.moveServiceListCursor(-1)
	model.moveServiceListCursor(-1)
	back := model.serviceListRows()[model.focusedServiceListRow()]
	if back.Kind != actionRowPreview || back.Continued {
		t.Fatalf("moving back landed on %#v", back)
	}
	model.moveServiceListCursor(-1)
	top := model.serviceListRows()[model.focusedServiceListRow()]
	if top.Kind != actionRowAction {
		t.Fatalf("the cursor did not reach the action row: %#v", top)
	}
}

func firstPreviewIndex(rows []actionListRow) int {
	for index, row := range rows {
		if row.Kind == actionRowPreview {
			return index
		}
	}
	return 0
}

func TestEveryLineOfACommandLooksTheSame(t *testing.T) {
	long := strings.Repeat("value-", 12)
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"g": {Actions: map[string]config.Action{
			"a": {
				Params:     map[string]config.ActionParam{"target": {Type: "text", Default: long, Arg: "--target"}},
				ParamOrder: []string{"target"},
				Run:        config.ArgvList{"./a.sh", "--with", "a-long-fixed-argument"},
				Dir:        t.TempDir(),
			},
		}, ActionOrder: []string{"a"}},
	}, ActionGroupOrder: []string{"g"}}, "test")
	defer func() { _ = model.Shutdown() }()
	model.width, model.height, model.ready = 100, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "g", Name: "a"}
	model.expandedActionOwner[actionOwnerKey(config.ActionOwnerGroup, "g")] = true
	model.expandedActionParams[id] = true

	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)

	// The style each command line dresses its text in has to be the same one,
	// or the continuation comes out a different shade from the first line.
	sequences := []string{}
	for _, row := range model.serviceListRows() {
		if row.Kind != actionRowPreview {
			continue
		}
		rendered := m1RenderRow(model, row)
		at := strings.Index(rendered, row.Preview[:4])
		if at <= 0 {
			t.Fatalf("command text not found in %q", rendered)
		}
		prefix := rendered[:at]
		sequences = append(sequences, prefix[strings.LastIndex(prefix, "\x1b["):])
	}
	if len(sequences) < 2 {
		t.Fatalf("expected a continued command, got %d line(s)", len(sequences))
	}
	for _, sequence := range sequences[1:] {
		if sequence != sequences[0] {
			t.Fatalf("command lines are styled differently: %q vs %q", sequences[0], sequence)
		}
	}
}

func m1RenderRow(model *Model, row actionListRow) string {
	for index, candidate := range model.serviceListRows() {
		if candidate.Kind == row.Kind && candidate.Preview == row.Preview && candidate.Continued == row.Continued {
			return model.renderServiceListRow(index, candidate, 80)
		}
	}
	return ""
}

func TestASwitchedOffParameterRecedes(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)

	model, id := parameterTreeModel(t)
	model.expandedActionParams[id] = true

	live := model.paramRowLabel(id, "env")
	model.toggleParamDisabled(id, "env")
	off := model.paramRowLabel(id, "env")
	// The name has to change with the value: a row where only the value moved
	// still reads as a live setting.
	if live == off {
		t.Fatalf("the name of a switched-off parameter looks the same: %q", ansi.Strip(off))
	}
	if off != ParamDisabledStyle.Render(ansi.Strip(off)) {
		t.Fatalf("the name is not drawn in the receded style: %q", off)
	}

	// In the form the values recede with it, and it says so before them.
	model.openParamForm(id)
	model.moveParamFormField(1)
	view := model.renderParamFormView()
	plain := ansi.Strip(view)
	envLine := ""
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "env") && strings.Contains(line, disabledParamLabel) {
			envLine = line
		}
	}
	if envLine == "" {
		t.Fatalf("the form does not mark the switched-off field:\n%s", plain)
	}
	if strings.Index(envLine, disabledParamLabel) > strings.Index(envLine, "dev") {
		t.Fatalf("the values come before the word that explains them: %q", envLine)
	}
	receded := ParamDisabledStyle.Render(disabledParamLabel)
	if !strings.Contains(view, receded) {
		t.Fatalf("the switched-off field is not drawn in the receded style:\n%q", view)
	}
	// Its values recede with it rather than staying lit next to the word.
	if !strings.Contains(view, ParamDisabledStyle.Render(markChosen+" dev")) {
		t.Fatalf("the values of a switched-off field stayed lit:\n%q", view)
	}
}

func TestFocusedDisabledParameterUsesSelectionForeground(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)

	model, id := parameterTreeModel(t)
	model.expandedActionParams[id] = true
	focusRow(t, model, "param:env")
	model.toggleParamDisabled(id, "env")

	inline := m1RenderRow(model, actionListRow{Kind: actionRowParam, Action: id, Param: "env"})
	disabledPrefix := terminalStylePrefix(ParamDisabledStyle)
	if disabledPrefix != "" && strings.Contains(inline, disabledPrefix) {
		t.Fatalf("focused inline parameter kept the disabled foreground: %q", inline)
	}

	model.openParamForm(id)
	spec := model.actionParamSpecs(id)[0]
	form := strings.Join(model.paramFormField(spec, 0, len(spec.Name), model.paramFormWidth()), "\n")
	if disabledPrefix != "" && strings.Contains(form, disabledPrefix) {
		t.Fatalf("focused form parameter kept the disabled foreground: %q", form)
	}
}

func TestAllDisabledCommandSelectorShowsDisabledInsteadOfABlankCommand(t *testing.T) {
	model := NewModel(&config.Config{Project: "Params", ActionGroups: map[string]config.ActionGroup{
		"checks": {Actions: map[string]config.Action{
			"check": {
				Params: map[string]config.ActionParam{
					"check": {
						Type:    "radio",
						Default: "lint",
						Options: config.ActionParamOptions{
							{Value: "lint", Run: config.ArgvList{"/bin/echo", "lint passed"}},
							{Value: "types", Run: config.ArgvList{"/bin/echo", "types passed"}},
						},
					},
				},
				ParamOrder: []string{"check"},
			},
		}, ActionOrder: []string{"check"}},
	}, ActionGroupOrder: []string{"checks"}}, "test")
	t.Cleanup(func() { _ = model.Shutdown() })
	model.width, model.height, model.ready = 120, 30, true
	id := config.ActionID{OwnerKind: config.ActionOwnerGroup, Owner: "checks", Name: "check"}
	model.toggleParamDisabled(id, "check")
	model.openParamForm(id)

	view := ansi.Strip(model.renderParamFormView())
	if !strings.Contains(view, "$ "+disabledParamLabel) {
		t.Fatalf("an all-disabled command selector rendered a blank command:\n%s", view)
	}
}
