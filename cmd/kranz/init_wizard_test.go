package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestInitDraftWritesOnlyChangedAppearanceFields(t *testing.T) {
	draft := newInitDraft(t.TempDir(), initOptions{})
	draft.Services = []initServiceDraft{{Name: "web", Dir: ".", Command: "run web"}}
	draft.AppearanceConfigured = true
	draft.Theme, draft.ThemeSet = "nord", true

	document, err := draft.document()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "theme: nord") {
		t.Fatalf("theme missing from draft:\n%s", rendered)
	}
	for _, unwanted := range []string{"accent:", "background:", "color_mode:"} {
		if strings.Contains(rendered, unwanted) {
			t.Errorf("unchanged appearance field %q was written:\n%s", unwanted, rendered)
		}
	}
}

func TestInitWizardAddsEditsAndDeletesDraftItems(t *testing.T) {
	model := newInitWizardModel(t.TempDir(), "kranz.yaml", initOptions{})
	model.screen = initWizardBuilder

	model.openServiceForm(-1)
	setInitFields(&model, "web", "./frontend", "npm run dev", "3000")
	updated, _ := model.submitForm()
	model = updated.(initWizardModel)
	if len(model.draft.Services) != 1 || model.draft.Services[0].Port != 3000 {
		t.Fatalf("service draft = %#v", model.draft.Services)
	}

	model.openActionForm(-1)
	setInitFields(&model, "test", "web", ".", "npm test")
	updated, _ = model.submitForm()
	model = updated.(initWizardModel)
	if len(model.draft.Actions) != 1 || model.draft.Actions[0].Owner != "web" {
		t.Fatalf("action draft = %#v", model.draft.Actions)
	}

	model.openServiceForm(0)
	setInitFields(&model, "frontend", "./web", "npm run start", "3100")
	updated, _ = model.submitForm()
	model = updated.(initWizardModel)
	if model.draft.Services[0].Name != "frontend" || model.draft.Actions[0].Owner != "frontend" {
		t.Fatalf("renaming service did not update its draft and action owner: %#v / %#v", model.draft.Services, model.draft.Actions)
	}

	model.cursor = 1 // appearance is row zero; the service follows it
	updated, _ = model.updateBuilder(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(initWizardModel)
	if len(model.draft.Services) != 0 || len(model.draft.Actions) != 0 {
		t.Fatalf("deleting a service left draft objects behind: %#v / %#v", model.draft.Services, model.draft.Actions)
	}
}

func TestInitWizardEnterAdvancesThenFocusesSave(t *testing.T) {
	model := newInitWizardModel(t.TempDir(), "kranz.yaml", initOptions{})
	model.openServiceForm(-1)
	setInitFields(&model, "web", ".", "run web", "3000")

	for want := 1; want < len(model.fields); want++ {
		updated, _ := model.updateForm(tea.KeyMsg{Type: tea.KeyEnter})
		model = updated.(initWizardModel)
		if model.fieldCursor != want || model.screen != initWizardService {
			t.Fatalf("Enter did not advance to field %d: cursor=%d screen=%v", want, model.fieldCursor, model.screen)
		}
	}
	updated, _ := model.updateForm(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(initWizardModel)
	if model.screen != initWizardService || model.formButton != 0 || len(model.draft.Services) != 0 {
		t.Fatalf("Enter on last field did not focus Save: screen=%v button=%d services=%#v", model.screen, model.formButton, model.draft.Services)
	}
	updated, _ = model.updateForm(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(initWizardModel)
	if model.formButton != 1 {
		t.Fatalf("Right did not focus Cancel: button=%d", model.formButton)
	}
	updated, _ = model.updateForm(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(initWizardModel)
	updated, _ = model.updateForm(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(initWizardModel)
	if model.screen != initWizardBuilder || len(model.draft.Services) != 1 {
		t.Fatalf("Enter on focused Save did not save service: screen=%v services=%#v", model.screen, model.draft.Services)
	}
}

func TestInitWizardBuilderShowsActionsAndSupportsMouseSelectionAndDelete(t *testing.T) {
	model := newInitWizardModel(t.TempDir(), "kranz.yaml", initOptions{})
	model.screen = initWizardBuilder
	model.width, model.height = 100, 40
	model.draft.Services = []initServiceDraft{{Name: "web", Dir: ".", Command: "run web", Port: 3000}}

	view := ansi.Strip(model.View())
	for _, want := range []string{"SERVICES", "Edit", "Delete", "APPEARANCE", "ACTIONS"} {
		if !strings.Contains(view, want) {
			t.Fatalf("builder is missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"[Edit]", "[Delete]", "[Add]"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("builder action %q should not use shortcut brackets:\n%s", unwanted, view)
		}
	}
	lines := strings.Split(view, "\n")
	rowY, deleteX := -1, -1
	for y, line := range lines {
		if strings.Contains(line, "web") && strings.Contains(line, "Delete") {
			rowY, deleteX = y, strings.Index(line, "Delete")
			break
		}
	}
	if rowY < 0 || deleteX < 0 {
		t.Fatalf("could not locate service controls:\n%s", view)
	}
	updated, _ := model.updateMouse(tea.MouseMsg{X: deleteX, Y: rowY, Action: tea.MouseActionMotion})
	model = updated.(initWizardModel)
	if model.cursor != 1 {
		t.Fatalf("hover selected row %d, want service row 1", model.cursor)
	}
	updated, _ = model.updateMouse(tea.MouseMsg{X: deleteX, Y: rowY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	model = updated.(initWizardModel)
	if len(model.draft.Services) != 0 {
		t.Fatalf("mouse Delete left services behind: %#v", model.draft.Services)
	}
}

func TestInitWizardAppearanceRetainsCustomColorsAcrossSourceChanges(t *testing.T) {
	model := newInitWizardModel(t.TempDir(), "kranz.yaml", initOptions{})
	model.openAppearance()
	model.cursor = 2
	model.changeAppearance(1)
	model.draft.CustomAccent = "#123456"
	model.changeAppearance(1)
	model.changeAppearance(1)
	if model.draft.AccentSource != "custom" || model.draft.CustomAccent != "#123456" {
		t.Fatalf("custom accent was not retained: source=%q color=%q", model.draft.AccentSource, model.draft.CustomAccent)
	}

	model.cursor = 3
	model.changeAppearance(1) // theme
	model.changeAppearance(1) // custom
	model.draft.CustomBackground = "#654321"
	model.changeAppearance(1) // terminal
	model.changeAppearance(-1)
	if model.draft.BackgroundSource != "custom" || model.draft.CustomBackground != "#654321" {
		t.Fatalf("custom background was not retained: source=%q color=%q", model.draft.BackgroundSource, model.draft.CustomBackground)
	}

	view := model.viewAppearance()
	for _, want := range []string{"KRANZ", "COLOR MODE", "auto", "background custom"} {
		if !strings.Contains(view, want) {
			t.Errorf("appearance preview is missing %q:\n%s", want, view)
		}
	}
	model.draft.Services = []initServiceDraft{{Name: "web", Dir: ".", Command: "run web"}}
	document, err := model.draft.document()
	if err != nil {
		t.Fatalf("custom background made the init draft invalid: %v", err)
	}
	rendered, err := renderDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "background: '#654321'") {
		t.Fatalf("custom background was not serialized:\n%s", rendered)
	}
}

func TestInitWizardAppearanceDoneReturnsToValidBuilderSelection(t *testing.T) {
	model := newInitWizardModel(t.TempDir(), "kranz.yaml", initOptions{})
	model.openAppearance()
	model.cursor = 5
	updated, _ := model.updateAppearance(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(initWizardModel)
	if model.screen != initWizardBuilder || model.cursor != 0 {
		t.Fatalf("Done returned to screen=%v cursor=%d, want builder cursor 0", model.screen, model.cursor)
	}
	updated, _ = model.updateBuilder(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(initWizardModel)
	if model.screen != initWizardAppearance {
		t.Fatalf("returned builder selection did not reopen Appearance: screen=%v", model.screen)
	}
}

func TestInitWizardExistingConfigurationStartsSafeAndReviewsDiff(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "kranz.yaml")
	if err := os.WriteFile(target, []byte("project: Before\nservices:\n  old:\n    command: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := newInitWizardModel(directory, "kranz.yaml", initOptions{})
	if model.screen != initWizardReplace || model.replaceCursor != 0 {
		t.Fatalf("existing file did not start on safe cancel choice: screen=%v cursor=%d", model.screen, model.replaceCursor)
	}

	model.draft.Project = "After"
	model.draft.Services = []initServiceDraft{{Name: "web", Dir: ".", Command: "new"}}
	model.screen = initWizardReview
	view := model.View()
	for _, want := range []string{"Replace kranz.yaml", "--- current", "+++ proposed", "- project: Before", "+ project: After"} {
		if !strings.Contains(view, want) {
			t.Errorf("replacement review is missing %q:\n%s", want, view)
		}
	}
}

func TestInitWizardProjectActionsRenderAsActionGroup(t *testing.T) {
	draft := newInitDraft(t.TempDir(), initOptions{})
	draft.Actions = []initActionDraft{{Name: "test", Owner: projectActionOwner, Dir: ".", Command: "make test"}}
	document, err := draft.document()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"action_groups:", "project:", "test:", "command: make test"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("project action YAML is missing %q:\n%s", want, rendered)
		}
	}
}

func TestInitWizardKeepsLargeDraftAndReviewNavigable(t *testing.T) {
	model := newInitWizardModel(t.TempDir(), "kranz.yaml", initOptions{})
	model.screen, model.height = initWizardBuilder, 12
	for index := 0; index < 12; index++ {
		model.draft.Services = append(model.draft.Services, initServiceDraft{
			Name:    "service-" + strconv.Itoa(index),
			Dir:     ".",
			Command: "run " + strconv.Itoa(index),
		})
	}
	model.cursor = len(model.builderRows()) - 1
	view := model.viewBuilder()
	if !strings.Contains(view, "Review and save") || !strings.Contains(view, "↑") {
		t.Fatalf("large builder did not keep the selected tail visible:\n%s", view)
	}

	model.screen, model.reviewOffset = initWizardReview, 0
	updated, _ := model.updateReview(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(initWizardModel)
	if model.reviewOffset != 1 {
		t.Fatalf("review offset = %d, want 1", model.reviewOffset)
	}
	for index := 0; index < 100; index++ {
		updated, _ = model.updateReview(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(initWizardModel)
	}
	if model.reviewOffset != model.maxReviewOffset() {
		t.Fatalf("review scrolling exceeded its bound: got %d want %d", model.reviewOffset, model.maxReviewOffset())
	}
}

func setInitFields(model *initWizardModel, values ...string) {
	for index, value := range values {
		model.fields[index].SetValue(value)
	}
}
