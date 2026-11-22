package ui

import (
	"fmt"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	"github.com/muesli/termenv"
)

func dependencyModalFixture(t *testing.T) *Model {
	t.Helper()
	model := NewModel(&config.Config{Services: map[string]config.Service{
		"gateway": {Command: "true", DependsOn: []string{"cache", "index"}},
		"cache": {
			Command: "true", DependsOn: []string{"storage"},
			DependencyConditions: map[string]config.DependencyConfig{"storage": {Condition: config.DependencyCompletedSuccessfully}},
		},
		"index": {
			Command: "true", DependsOn: []string{"storage"},
			DependencyConditions: map[string]config.DependencyConfig{"storage": {Condition: config.DependencyStarted}},
		},
		"storage": {Command: "true"},
		"worker":  {Command: "true", DependsOn: []string{"gateway"}},
		"report":  {Command: "true", DependsOn: []string{"worker"}},
		"archive": {Command: "true"},
	}}, "test")
	t.Cleanup(func() { _ = model.Shutdown() })
	model.width, model.height, model.ready = 74, 20, true
	for index, svc := range model.services {
		if svc.Name == "gateway" {
			model.focused = index
			break
		}
	}
	return model
}

func dependencyBodyText(model *Model) string {
	lines, _ := model.dependencyBody()
	return ansi.Strip(strings.Join(lines, "\n"))
}

func TestDependencyModalGraphDirectionsAndScrolling(t *testing.T) {
	model := dependencyModalFixture(t)
	model.width = 82
	pressKey(model, 'g')
	if model.mode != ModeDependencies || model.dependencyModal.plan || strings.Join(model.dependencyModal.roots, ",") != "gateway" {
		t.Fatal("g did not open the graph for the focused service")
	}
	model.dependencyModal.graph.Edges = append(model.dependencyModal.graph.Edges, app.GraphEdge{
		From: "archive", To: "gateway", Type: app.GraphEdgePrerequisite,
	}, app.GraphEdge{
		From: "storage", To: "cache", Type: app.GraphEdgeDependency,
	})
	model.app.SetServiceStatusForTest("storage", config.StatusRunning)
	model.refreshServices()
	body := dependencyBodyText(model)
	previous := -1
	for _, want := range []string{"↑ Dependencies", "cache", "◆─ ○ gateway", "worker", "report", "↓ Dependents"} {
		index := strings.Index(body, want)
		if index <= previous {
			t.Fatalf("graph has %q missing or out of order:\n%s", want, body)
		}
		previous = index
	}
	for _, want := range []string{
		"● storage  · running", "required condition completed", "successfully", "↪ shared", "┴", "┬",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("graph missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "archive") {
		t.Fatalf("graph included a prerequisite edge:\n%s", body)
	}
	if count := strings.Count(body, "● storage"); count != 2 {
		t.Fatalf("graph showed %d storage rows for two distinct dependency links:\n%s", count, body)
	}

	model.width, model.height = 48, 14
	bodyRows, rootRow := model.dependencyBody()
	page := model.dependencyBodyHeight(len(model.dependencyFooter()))
	initialOffset, maxOffset := model.dependencyViewport(len(bodyRows), rootRow, page)
	if initialOffset <= 0 || !strings.Contains(ansi.Strip(model.dependencyModalContent()), "◆─ ○ gateway") {
		t.Fatal("narrow graph did not initially frame the root")
	}
	pressKey(model, 'j')
	model.handleMouseMsg(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if want := max(0, min(maxOffset, initialOffset+1)-1); model.dependencyModal.offset != want {
		t.Fatalf("j and wheel left the graph at offset %d", model.dependencyModal.offset)
	}
	if frame := model.View(); lipgloss.Width(frame) != model.width || lipgloss.Height(frame) != model.height {
		t.Fatalf("narrow frame measured %dx%d, want %dx%d", lipgloss.Width(frame), lipgloss.Height(frame), model.width, model.height)
	}
	clickRenderedText(t, model, "[d] Dependencies")
	body = dependencyBodyText(model)
	if model.dependencyModal.offset != -1 || strings.Contains(body, "cache") || !strings.Contains(body, "worker") || !strings.Contains(body, "◆─ ○ gateway") {
		t.Fatalf("hiding dependencies changed the wrong direction or retained the scroll offset:\n%s", body)
	}
	pressKey(model, 'u')
	if body := dependencyBodyText(model); !strings.Contains(body, "Both directions are hidden.") || strings.Contains(body, "worker") {
		t.Fatalf("graph did not show its empty state:\n%s", body)
	}
	pressKey(model, 'd')
	if body := dependencyBodyText(model); !strings.Contains(body, "cache") || strings.Contains(body, "worker") {
		t.Fatalf("showing dependencies restored the wrong direction:\n%s", body)
	}
	pressEsc(model)
	if model.mode != ModeNormal {
		t.Fatal("Esc did not close the graph")
	}
}

func TestDependencyModalGraphShowsSharedNeighborsAndEdgesBetweenRoots(t *testing.T) {
	model := dependencyModalFixture(t)
	model.width = 92
	model.selected["cache"], model.selected["gateway"], model.selected["index"] = true, true, true
	pressKey(model, 'g')
	if !model.dependencyModal.plan {
		t.Fatal("multiple checked services should open the plan")
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyTab})
	body := dependencyBodyText(model)
	for _, want := range []string{
		"◆─ [1] ○ cache", "◆─ [2] ○ gateway", "◆─ [3] ○ index",
		"required by [1] (completed successfully), [3]", "(started)", "[2] requires [1]", "[2] requires [3]",
		"○ worker  · requires [2]",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("multi-root graph missing %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "○ storage") != 1 || strings.Count(body, "○ cache") != 1 ||
		strings.Index(body, "storage") > strings.Index(body, "◆─ [1]") {
		t.Fatalf("shared immediate dependencies or roots were repeated or misplaced:\n%s", body)
	}
}

func TestDependencyModalPreviewsSelectionWithoutRunningIt(t *testing.T) {
	model := dependencyModalFixture(t)
	model.selected["gateway"], model.selected["worker"] = true, true
	pressKey(model, 'g')
	if !model.dependencyModal.plan {
		t.Fatal("multiple checked services should open the plan")
	}
	content := ansi.Strip(model.dependencyModalContent())
	body := dependencyBodyText(model)
	for _, want := range []string{"Requested\n    ○ gateway\n    ○ worker", "Wave 1", "└─ ○ storage  · dependency", "Wave 2", "Wave 3", "Wave 4", "└─ ○ worker"} {
		if !strings.Contains(body, want) {
			t.Errorf("start plan missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "Wave 4\n      └─ ○ worker\n         ╰─ needs gateway") || strings.Count(body, "needs gateway") != 1 ||
		strings.Contains(body, "Readiness gates are skipped.") {
		t.Fatalf("the wave did not show its link directly under the service:\n%s", body)
	}
	if !strings.Contains(content, "Operation Plan · Start") || !strings.Contains(content, "[f] Selected only ○") {
		t.Fatalf("start or default dependency setting was missing:\n%s", content)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyTab})
	if model.dependencyModal.plan || strings.Join(model.dependencyModal.roots, ",") != "gateway,worker" {
		t.Fatal("Tab did not open the selected services' graph")
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyTab})
	pressKey(model, 'f')
	body = dependencyBodyText(model)
	if !strings.Contains(body, "1. ○ gateway") || !strings.Contains(body, "2. ○ worker") || strings.Contains(body, "storage") {
		t.Fatalf("selected-only start plan is not a sequence of the checked services:\n%s", body)
	}
	if !strings.Contains(body, "2. ○ worker\n       ╰─ needs gateway") ||
		!strings.Contains(body, "Selected dependencies go first. Readiness gates are skipped.") || strings.Count(body, "needs gateway") != 1 {
		t.Fatalf("selected-only sequence did not preserve the descriptive link:\n%s", body)
	}
	for _, operation := range []rune{'x', 'r', 's'} {
		_, cmd := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{operation}})
		if cmd != nil || len(model.operations) != 0 {
			t.Fatalf("%c started an operation from the preview", operation)
		}
	}
	pressKey(model, 'x')
	body = dependencyBodyText(model)
	if !strings.Contains(body, "No service changes") {
		t.Fatalf("stop plan did not report that the selected services were stopped:\n%s", body)
	}
	if !strings.Contains(body, "○ worker\n    ╰─ needs gateway") {
		t.Fatalf("idle plan omitted the relationship between requested services:\n%s", body)
	}
	model.app.SetServiceStatusForTest("worker", config.StatusRunning)
	model.app.SetServiceStatusForTest("report", config.StatusRunning)
	model.refreshServices()
	pressKey(model, 'f')
	pressKey(model, 'r')
	body = dependencyBodyText(model)
	if !strings.Contains(body, "1. ● report  · running  · dependent") || !strings.Contains(body, "3. ● report  · running  · dependent") {
		t.Fatalf("restart plan did not show the expanded dependent in both phases:\n%s", body)
	}
	clickRenderedText(t, model, "[Esc] Close")
	if model.mode != ModeNormal {
		t.Fatal("clicking Close did not dismiss the plan")
	}
	model.focusedActionGroup = "automation"
	clear(model.selected)
	pressKey(model, 'g')
	if model.dependencyModal.plan || len(model.dependencyModal.roots) != 0 {
		t.Fatal("an action group took the previous service's graph or plan")
	}
	pressEsc(model)
	model.selected["gateway"] = true
	pressKey(model, 'g')
	if !model.dependencyModal.plan {
		t.Fatal("an explicit selection should open the plan from a non-service row")
	}
}

func TestDependencyModalSelectedOnlyListsPrerequisiteFirst(t *testing.T) {
	model := dependencyModalFixture(t)
	model.selected["cache"], model.selected["storage"] = true, true
	pressKey(model, 'g')
	pressKey(model, 'f')
	body := dependencyBodyText(model)
	if !strings.Contains(body, "1. ○ storage\n    2. ○ cache\n       ╰─ needs storage") {
		t.Fatalf("selected-only start should put the selected dependency first and attach its link:\n%s", body)
	}
}

func TestDependencySequenceColorsNumbersAndAttachments(t *testing.T) {
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.TrueColor)
	lines := appendDependencySequence(nil, []string{"storage", "cache"}, map[string][]string{"cache": {"storage"}},
		func(name string) string { return "○ " + name }, 48, renderer)
	first := dependencyDepthStyle(renderer, 0, 2)
	second := dependencyDepthStyle(renderer, 1, 2)
	if len(lines) != 3 || !strings.Contains(lines[0], first.Bold(true).Render("1.")+" ○ storage") ||
		!strings.Contains(lines[1], second.Bold(true).Render("2.")+" ○ cache") ||
		!strings.Contains(lines[2], second.Render("╰─ ")) {
		t.Fatalf("sequence did not color each number and its attachment together:\n%q", lines)
	}
	if first.Render("│") == second.Render("│") {
		t.Fatal("sequence did not distinguish the first and last steps")
	}
}

func TestDependencyGradientTracksThemeAndAccent(t *testing.T) {
	restoreDefaultTheme(t)
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.TrueColor)
	endpoints := func(themeName, accent string) [2]string {
		t.Helper()
		if _, err := ApplyTheme(themeName, accent); err != nil {
			t.Fatal(err)
		}
		return [2]string{
			dependencyDepthStyle(renderer, 0, 2).Render("│"),
			dependencyDepthStyle(renderer, 1, 2).Render("│"),
		}
	}
	original := endpoints(DefaultTheme, "")
	alternate := endpoints("dracula", "")
	custom := endpoints("dracula", "#C08457")
	for index := range original {
		if original[index] == alternate[index] || alternate[index] == custom[index] {
			t.Errorf("endpoint %d did not update with both the theme and accent", index+1)
		}
	}
}

func TestDependencyWaveColorsFollowTerminalProfile(t *testing.T) {
	for _, test := range []struct {
		name      string
		profile   termenv.Profile
		sgrPrefix string
		minColors int
		maxColors int
	}{
		{"truecolor", termenv.TrueColor, "\x1b[38;2;", 4, 4},
		{"ansi256", termenv.ANSI256, "\x1b[38;5;", 1, 4},
		{"ansi16", termenv.ANSI, "\x1b[", 1, 4},
		{"monochrome", termenv.Ascii, "", 1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			renderer := lipgloss.NewRenderer(io.Discard)
			renderer.SetColorProfile(test.profile)
			colors := make(map[string]bool)
			for index := range 4 {
				style := dependencyDepthStyle(renderer, index, 4)
				heading := fmt.Sprintf("Wave %d", index+1)
				renderedHeading := style.Bold(true).Render(heading)
				rail := style.Render("└─")
				if ansi.Strip(renderedHeading) != heading || ansi.Strip(rail) != "└─" {
					t.Fatalf("drawing changed wave text or rail: %q %q", renderedHeading, rail)
				}
				if test.profile == termenv.Ascii {
					if renderedHeading != heading || rail != "└─" {
						t.Fatalf("monochrome drawing contains terminal escapes: %q %q", renderedHeading, rail)
					}
				} else if !strings.HasPrefix(rail, test.sgrPrefix) {
					t.Fatalf("rail has the wrong color encoding: %q", rail)
				}
				colors[rail] = true
			}
			if len(colors) < test.minColors || len(colors) > test.maxColors {
				t.Fatalf("rendered %d distinct rail colors, want %d–%d", len(colors), test.minColors, test.maxColors)
			}
		})
	}
}
