package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/kranz-org/kranz/internal/config"
)

func TestMainViewExposesPrimaryActionAndReadableState(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	for _, terminalWidth := range []int{64, 80, 100, 120} {
		model.width, model.height, model.ready = terminalWidth, 28, true
		rendered := model.renderMainView()
		plain := ansi.Strip(rendered)
		for _, expected := range []string{"KRANZ", "SERVICES", "DETAILS", "LOGS", "Start", "Select", "●", "READINESS", "LIVENESS"} {
			if !strings.Contains(plain, expected) {
				t.Errorf("width %d: render does not contain %q", terminalWidth, expected)
			}
		}
		for lineNumber, line := range strings.Split(rendered, "\n") {
			if width := lipgloss.Width(line); width > model.width {
				t.Errorf("terminal %d, line %d width = %d", terminalWidth, lineNumber, width)
			}
		}
		if height := lipgloss.Height(rendered); height != model.height {
			t.Errorf("terminal %d: render height = %d, want %d", terminalWidth, height, model.height)
		}
		if strings.HasSuffix(rendered, "\n") {
			t.Errorf("terminal %d: render has an extra trailing row", terminalWidth)
		}
	}
	if action := model.actionAt(1); action != "toggle" {
		t.Fatalf("primary button action = %q", action)
	}
}

func TestStatusBarSeparatesEveryAction(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width = 120
	bar := ansi.Strip(model.renderStatusBar())
	want := len(model.actionButtons()) - 1
	if got := strings.Count(bar, "│"); got != want {
		t.Fatalf("status separators = %d, want %d:\n%s", got, want, bar)
	}
	for _, button := range model.actionButtons() {
		if label := ansi.Strip(button.rendered); !strings.Contains(label, ": ") {
			t.Errorf("action label does not use lazygit key separator: %q", label)
		}
	}
}

func TestMouseActivatesDashboardAndModalControls(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 30, true

	clickRenderedText(t, model, "[1]")
	if model.listMode != listTags {
		t.Fatal("clicking the focused [1] title did not switch to tags")
	}
	clickRenderedText(t, model, "[ ] ▸ backend")
	if len(model.selectedTags) != 1 || model.selectedTags[0] != "backend" {
		t.Fatalf("clicking a tag checkbox selected %v", model.selectedTags)
	}
	clickRenderedText(t, model, "[2]")
	if model.panelFocus != panelDetails {
		t.Fatal("clicking [2] did not focus details")
	}
	clickRenderedText(t, model, "[3]")
	if model.panelFocus != panelLogs {
		t.Fatal("clicking [3] did not focus logs")
	}
	clickRenderedText(t, model, "[/] regex")
	if model.mode != ModeSearch {
		t.Fatal("clicking the regex control did not open search")
	}
	clickRenderedText(t, model, "[Tab]")
	if model.searchMode != searchHighlight {
		t.Fatal("clicking the search mode control did not toggle it")
	}
	clickRenderedText(t, model, "[Esc] done")
	if model.mode != ModeNormal {
		t.Fatal("clicking the search exit control did not return to the dashboard")
	}
	clickRenderedText(t, model, "[?] help")
	if model.mode != ModeHelp {
		t.Fatal("clicking help did not open it")
	}
	clickRenderedText(t, model, "[Esc] Close")
	if model.mode != ModeNormal {
		t.Fatal("clicking modal close did not return to the dashboard")
	}
}

func TestServiceStatusUsesQueuedAndRuntimeVisualStates(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	svc := model.FocusedService()

	if state := model.serviceVisualState(svc); state != visualStopped {
		t.Fatalf("stopped service visual state = %v", state)
	}
	svc.Config.DependsOn = []string{"database"}
	svc.SetDesiredRunning(true)
	if state := model.serviceVisualState(svc); state != visualQueued {
		t.Fatalf("queued service visual state = %v", state)
	}
	if line := ansi.Strip(model.renderServiceLine(model.focused, svc, 50)); !strings.Contains(line, "queued") {
		t.Fatalf("queued service line = %q", line)
	}
	if details := ansi.Strip(strings.Join(model.serviceDetailLines(svc, 80), "\n")); !strings.Contains(details, "Queued") || !strings.Contains(details, "Waiting for dependencies: database") {
		t.Fatalf("queued service details:\n%s", details)
	}
	if _, pending, _ := model.serviceCounts(); pending != 1 {
		t.Fatalf("queued service pending count = %d", pending)
	}
	if controls := ansi.Strip(model.renderStatusBar()); !strings.Contains(controls, "Stop") {
		t.Fatalf("queued service controls = %q", controls)
	}
	svc.SetDesiredRunning(false)
	svc.SetStatus(config.StatusRunning)
	if state := model.serviceVisualState(svc); state != visualRunning {
		t.Fatalf("running service visual state = %v", state)
	}
	svc.Config.HealthCheck = &config.HealthCheckConfig{Readiness: &config.CheckConfig{}}
	if state := model.serviceVisualState(svc); state != visualStarting {
		t.Fatalf("waiting service visual state = %v", state)
	}
	svc.SetStatus(config.StatusUnhealthy)
	if state := model.serviceVisualState(svc); state != visualUnhealthy {
		t.Fatalf("unhealthy service visual state = %v", state)
	}
}

func TestMainViewUsesEveryTerminalRow(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	for _, terminalHeight := range []int{14, 18, 24, 32} {
		model.width, model.height, model.ready = 80, terminalHeight, true
		rendered := model.renderMainView()
		if height := lipgloss.Height(rendered); height != terminalHeight {
			t.Errorf("terminal height %d rendered as %d rows", terminalHeight, height)
		}
	}
}

func TestPanelFocusUsesNumbersAndContextualArrows(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if model.panelFocus != panelDetails {
		t.Fatalf("panel focus = %v, want details", model.panelFocus)
	}
	serviceIndex := model.focused
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyDown})
	if model.focused != serviceIndex {
		t.Fatal("details scrolling changed the focused service")
	}

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyDown})
	if model.focused != serviceIndex+1 {
		t.Fatal("service-panel down did not move the service cursor")
	}

	for index := range 40 {
		model.FocusedService().AppendLog(fmt.Sprintf("line %d", index))
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyUp})
	if model.panelFocus != panelLogs || model.logOffset == 0 || model.followMode {
		t.Fatalf("log focus/scroll state = panel %v offset %d follow %v", model.panelFocus, model.logOffset, model.followMode)
	}
}

func TestHorizontalArrowsCycleServicesAndTagsOnlyInListPanel(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	if model.listMode != listServices {
		t.Fatalf("initial list mode = %v, want services", model.listMode)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRight})
	if model.listMode != listTags {
		t.Fatalf("Right list mode = %v, want tags", model.listMode)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyLeft})
	if model.listMode != listServices {
		t.Fatalf("Left list mode = %v, want services", model.listMode)
	}

	model.panelFocus = panelDetails
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRight})
	if model.listMode != listServices {
		t.Fatalf("Right outside list panel changed mode to %v", model.listMode)
	}
}

func TestTabCyclesPanelsAndIncludesPinnedLogs(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	for _, expected := range []panelFocus{panelDetails, panelLogs, panelServices} {
		_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyTab})
		if model.panelFocus != expected {
			t.Fatalf("Tab focused panel %v, want %v", model.panelFocus, expected)
		}
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyShiftTab})
	if model.panelFocus != panelLogs {
		t.Fatalf("Shift+Tab focused panel %v, want logs", model.panelFocus)
	}

	model.panelFocus = panelServices
	pressKey(model, '#')
	for _, expected := range []panelFocus{panelDetails, panelLogs, panelPinnedLogs, panelServices} {
		_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyTab})
		if model.panelFocus != expected {
			t.Fatalf("Tab with pinned logs focused panel %v, want %v", model.panelFocus, expected)
		}
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyShiftTab})
	if model.panelFocus != panelPinnedLogs {
		t.Fatalf("Shift+Tab with pinned logs focused panel %v, want pinned logs", model.panelFocus)
	}
}

func TestOneFocusesListThenTogglesServicesAndTags(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.panelFocus = panelLogs

	pressKey(model, '1')
	if model.panelFocus != panelServices || model.listMode != listServices {
		t.Fatalf("first 1 = panel %v mode %v, want focused services", model.panelFocus, model.listMode)
	}
	pressKey(model, '1')
	if model.listMode != listTags {
		t.Fatalf("second 1 mode = %v, want tags", model.listMode)
	}
	pressKey(model, '1')
	if model.listMode != listServices {
		t.Fatalf("third 1 mode = %v, want services", model.listMode)
	}
}

func TestTagsExpandServicesInlineAndToggleWithEnter(t *testing.T) {
	model := NewModel(&config.Config{
		Project: "Tag details",
		Services: map[string]config.Service{
			"database": {
				Command: "exit 0", Dir: ".", Shell: "sh", Ports: []int{5432}, Tags: []string{"data", "core"},
			},
			"api": {
				Command: "exit 0", Dir: ".", Shell: "sh", Ports: []int{8080},
				Tags: []string{"backend", "core"}, DependsOn: []string{"database"},
			},
			"worker": {
				Command: "exit 0", Dir: ".", Shell: "sh",
				Tags: []string{"backend", "jobs"}, DependsOn: []string{"database"},
			},
		},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 30, true

	pressKey(model, 't')
	for index, row := range model.tagRows() {
		if row.Tag == "backend" {
			model.tagCursor = index
			break
		}
	}
	plain := ansi.Strip(model.renderServiceColumn(48, model.height-2))
	for _, expected := range []string{
		"TAG DETAILS", "#backend", "2 services", "api · stopped · :8080",
		"worker · stopped", "PORTS 8080", "RELATED", "core", "jobs",
		"DEPENDS database",
	} {
		if !strings.Contains(plain, expected) {
			t.Errorf("tag details do not contain %q:\n%s", expected, plain)
		}
	}

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if command != nil {
		t.Fatal("tag expansion scheduled an unexpected command")
	}
	if model.listMode != listTags || !model.expandedTags["backend"] || len(model.services) != len(model.allServices) {
		t.Fatalf("expansion = mode %v expanded %v services %d/%d",
			model.listMode, model.expandedTags["backend"], len(model.services), len(model.allServices))
	}

	plain = ansi.Strip(model.renderServiceColumn(48, model.height-2))
	for _, expected := range []string{"▾ backend (2)", "api", "worker"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("expanded tag does not contain %q:\n%s", expected, plain)
		}
	}

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyDown})
	if svc := model.focusedTagService(); svc == nil || svc.Name != "api" {
		t.Fatalf("first expanded child = %v, want api", svc)
	}
	plain = ansi.Strip(model.renderServiceColumn(48, model.height-2))
	if !strings.Contains(plain, "DETAILS") || !strings.Contains(plain, "● api  Stopped") || strings.Contains(plain, "TAG DETAILS") {
		t.Fatalf("child focus did not switch to service details:\n%s", plain)
	}

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if !model.selected["api"] || len(model.selectedTags) != 0 {
		t.Fatalf("child selection = services %v tags %v", model.selected, model.selectedTags)
	}
	if targets := model.selectedTargetNames(); len(targets) != 1 || targets[0] != "api" {
		t.Fatalf("expanded child targets = %v", targets)
	}

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyUp})
	if model.focusedTagService() != nil || model.focusedTag() != "backend" {
		t.Fatalf("up from child did not return to backend tag")
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if model.expandedTags["backend"] {
		t.Fatal("second Enter did not collapse backend")
	}
	plain = ansi.Strip(model.renderServicePanel(48, 12))
	if strings.Contains(plain, "▾ backend") {
		t.Fatalf("collapsed tag still renders expanded disclosure:\n%s", plain)
	}

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if !model.selected["api"] || !model.selected["worker"] || len(model.selected) != 2 {
		t.Fatalf("selecting backend selected services %v", model.selected)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	plain = ansi.Strip(model.renderServicePanel(48, 12))
	for _, expected := range []string{"[✓] ▾ backend", "[✓] ● api", "[✓] ● worker"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("selected tag expansion does not contain %q:\n%s", expected, plain)
		}
	}
}

func TestMovingTagCursorChangesDetailsWithoutMovingServiceFocus(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	pressKey(model, 't')
	serviceIndex := model.focused
	firstTag := model.focusedTag()
	model.detailOffset = 3

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyDown})
	if model.focused != serviceIndex {
		t.Fatalf("tag navigation moved service focus from %d to %d", serviceIndex, model.focused)
	}
	if model.focusedTag() == firstTag {
		t.Fatalf("tag cursor did not move from %q", firstTag)
	}
	if model.detailOffset != 0 {
		t.Fatalf("tag navigation kept stale detail offset %d", model.detailOffset)
	}
}

func TestServiceColumnShowsUpToTwentyItems(t *testing.T) {
	services := make(map[string]config.Service, 25)
	for index := range 25 {
		services[fmt.Sprintf("service-%02d", index)] = config.Service{Command: "exit 0", Dir: ".", Shell: "sh"}
	}
	model := NewModel(&config.Config{Project: "Layout", Services: services}, "test")
	defer model.Shutdown()

	listHeight, detailHeight := model.serviceColumnLayout(100)
	if listHeight != 23 || detailHeight != 77 {
		t.Fatalf("25-item, 100-row column split = %d/%d, want 23/77", listHeight, detailHeight)
	}
	model.services = model.services[:2]
	listHeight, detailHeight = model.serviceColumnLayout(100)
	if listHeight != 6 || detailHeight != 94 {
		t.Fatalf("2-item, 100-row column split = %d/%d, want compact 6/94", listHeight, detailHeight)
	}
	model.services = model.allServices
	listHeight, detailHeight = model.serviceColumnLayout(22)
	if listHeight != 11 || detailHeight != 11 {
		t.Fatalf("25-item, 22-row column split = %d/%d, want balanced 11/11", listHeight, detailHeight)
	}
}

func TestCompactDashboardCollapsesInactivePanels(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, compactDashboardHeight, true
	panelHeight := model.height - dashboardHeaderRows - dashboardFooterRows

	listHeight, detailHeight := model.serviceColumnLayout(panelHeight)
	if listHeight != panelHeight-collapsedPanelHeight || detailHeight != collapsedPanelHeight {
		t.Fatalf("compact service split = %d/%d, want %d/%d", listHeight, detailHeight, panelHeight-collapsedPanelHeight, collapsedPanelHeight)
	}
	if renderedHeight := lipgloss.Height(model.renderServiceColumn(40, panelHeight)); renderedHeight != panelHeight {
		t.Fatalf("compact service column height = %d, want %d", renderedHeight, panelHeight)
	}

	model.panelFocus = panelDetails
	listHeight, detailHeight = model.serviceColumnLayout(panelHeight)
	if listHeight != collapsedPanelHeight || detailHeight != panelHeight-collapsedPanelHeight {
		t.Fatalf("focused detail split = %d/%d, want %d/%d", listHeight, detailHeight, collapsedPanelHeight, panelHeight-collapsedPanelHeight)
	}

	pressKey(model, '#')
	model.panelFocus = panelLogs
	pinnedHeight, currentHeight := model.logColumnLayout(panelHeight)
	if pinnedHeight != collapsedPanelHeight || currentHeight != panelHeight-collapsedPanelHeight {
		t.Fatalf("compact log split = %d/%d, want %d/%d", pinnedHeight, currentHeight, collapsedPanelHeight, panelHeight-collapsedPanelHeight)
	}
	model.panelFocus = panelPinnedLogs
	pinnedHeight, currentHeight = model.logColumnLayout(panelHeight)
	if pinnedHeight != panelHeight-collapsedPanelHeight || currentHeight != collapsedPanelHeight {
		t.Fatalf("focused pinned split = %d/%d, want %d/%d", pinnedHeight, currentHeight, panelHeight-collapsedPanelHeight, collapsedPanelHeight)
	}
	if renderedHeight := lipgloss.Height(model.renderLogColumn(60, panelHeight)); renderedHeight != panelHeight {
		t.Fatalf("compact log column height = %d, want %d", renderedHeight, panelHeight)
	}

	collapsed := ansi.Strip(renderCollapsedPanel("[2] DETAILS", 40))
	if !strings.HasPrefix(collapsed, " [2] DETAILS") || lipgloss.Width(collapsed) != 40 {
		t.Fatalf("collapsed title is not aligned inside its 40-column panel: %q", collapsed)
	}
}

func TestMouseWheelMovesThroughServices(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 24, true

	start := model.focused
	_, _ = model.handleMouseMsg(tea.MouseMsg{X: 1, Y: dashboardHeaderRows + 1, Button: tea.MouseButtonWheelDown})
	if model.panelFocus != panelServices || model.focused != start+1 {
		t.Fatalf("wheel down over services focused panel %v and service %d, want %v and %d", model.panelFocus, model.focused, panelServices, start+1)
	}

	_, _ = model.handleMouseMsg(tea.MouseMsg{X: 1, Y: dashboardHeaderRows + 1, Button: tea.MouseButtonWheelUp})
	if model.focused != start {
		t.Fatalf("wheel up over services focused service %d, want %d", model.focused, start)
	}
}

func TestServiceAndDetailsTitlesSeparateMetadata(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	services := ansi.Strip(model.renderServicePanel(48, 8))
	if !strings.Contains(services, "[1] SERVICES │ 2 · 1 → Tags") {
		t.Fatalf("service title does not separate metadata:\n%s", services)
	}

	details := ansi.Strip(model.renderServiceDetails(model.FocusedService(), 48, 8))
	if !strings.Contains(details, "[2] DETAILS │ ") {
		t.Fatalf("details title does not separate scroll metadata:\n%s", details)
	}

	model.listMode = listTags
	tags := ansi.Strip(model.renderTagPanel(48, 8))
	if !strings.Contains(tags, "[1] TAGS │ 2 · Enter expand") {
		t.Fatalf("tag title does not separate metadata:\n%s", tags)
	}
}

func TestHelpOverlaysDimmedDashboard(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	rendered := model.renderHelpView()
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "KRANZ") || !strings.Contains(plain, "Kranz Help") {
		t.Fatalf("help is not composited over the dashboard:\n%s", plain)
	}
	if lipgloss.Height(rendered) != model.height {
		t.Fatalf("help height = %d, want %d", lipgloss.Height(rendered), model.height)
	}
}

func TestHelpWrapsDescriptionsAndScrollsWithoutTruncatingThem(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	body := ansi.Strip(strings.Join(model.helpBodyLines(), "\n"))
	for _, description := range []string{
		"Focus panels; 1 switches Services/Tags when the list is focused",
		"Pin focused service logs above the active log panel",
		"Regex filter; Tab switches to highlight",
		"Choose and persist a theme",
	} {
		if rebuilt := strings.Join(wrapHelpText(description, 24), " "); rebuilt != description {
			t.Errorf("wrapped help rebuilt %q as %q", description, rebuilt)
		}
	}
	if strings.Contains(body, "…") {
		t.Fatalf("help still truncates descriptions:\n%s", body)
	}
	widest := 0
	for _, line := range model.helpBodyLines() {
		width := lipgloss.Width(line)
		widest = max(widest, width)
		if width > 74 {
			t.Fatalf("help line width = %d, want at most 74: %q", width, ansi.Strip(line))
		}
	}
	if widest <= 66 {
		t.Fatalf("help did not become materially wider: widest line = %d", widest)
	}

	model.mode = ModeHelp
	initial := ansi.Strip(model.renderHelpView())
	if !strings.Contains(initial, "[↑/k] Up") || !strings.Contains(initial, "[↓/j] Down") || lipgloss.Height(model.renderHelpView()) != model.height {
		t.Fatalf("scrollable help layout is invalid:\n%s", initial)
	}
	for range model.maxHelpOffset() {
		_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyDown})
	}
	if model.helpOffset != model.maxHelpOffset() {
		t.Fatalf("help offset = %d, want %d", model.helpOffset, model.maxHelpOffset())
	}
}

func TestHelpUsesTheWiderLimitAndRespectsTerminalBackground(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	dark := true
	model := NewModelWithOptions(&config.Config{
		Project: "Terminal", UI: config.UIConfig{Theme: "forest", Background: backgroundTerminal},
		Services: map[string]config.Service{"api": {Command: "exit 0"}},
	}, "test", ModelOptions{DarkBackground: &dark})
	defer model.Shutdown()
	model.width, model.height, model.ready = 120, 32, true

	widest := 0
	for _, line := range model.helpBodyLines() {
		widest = max(widest, lipgloss.Width(line))
	}
	if widest <= 100 || widest > 105 {
		t.Fatalf("help body width = %d, want the new 101–105 cell range", widest)
	}
	if _, ok := ModalStyle.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatalf("terminal-owned help still paints theme background %#v", ModalStyle.GetBackground())
	}
	if _, ok := ModalTitleStyle.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatalf("terminal-owned help title still paints theme background %#v", ModalTitleStyle.GetBackground())
	}

	painted, err := ApplyTheme(DefaultTheme, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ModalStyle.GetBackground(), lipgloss.Color(painted.SurfaceAlt)) {
		t.Fatalf("painted help background = %#v, want %s", ModalStyle.GetBackground(), painted.SurfaceAlt)
	}
}

func TestManualConfigReloadReconcilesModel(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "kranz.yaml")
	write := func(command string) {
		t.Helper()
		data := "project: Reload Test\nservices:\n  api:\n    command: " + command + "\n"
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("sleep 60")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	model := NewModelWithOptions(cfg, "test", ModelOptions{ConfigPaths: []string{path}})
	defer model.Shutdown()
	write("sleep 61")
	command := model.reloadConfig(true)
	if command == nil {
		t.Fatal("manual reload did not schedule a command")
	}
	message := command().(configReloadMsg)
	_, _ = model.handleConfigReload(message)
	if message.err != nil {
		t.Fatalf("reload message error = %v", message.err)
	}
	if got := model.FocusedService().Config.Command; got != "sleep 61" {
		t.Fatalf("reloaded command = %q", got)
	}
	if model.reloadBusy {
		t.Fatal("reload remained busy")
	}
}

func TestZshCommandShellBindsCtrlOAndPreservesEnvironment(t *testing.T) {
	if _, err := os.Stat("/bin/zsh"); err != nil {
		t.Skip("zsh is unavailable")
	}
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("ZDOTDIR", "/tmp/user-zdotdir")
	command, cleanup, err := commandShell()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if filepath.Base(command.Path) != "zsh" || !containsString(command.Args, "-i") {
		t.Fatalf("command = %#v", command.Args)
	}
	tempDir := environmentValue(command.Env, "ZDOTDIR")
	rc, err := os.ReadFile(filepath.Join(tempDir, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rc), "bindkey -s '^O' 'exit\\n'") {
		t.Fatalf("Ctrl+O binding missing from zsh rc:\n%s", rc)
	}
	if got := environmentValue(command.Env, "KRANZ_ORIGINAL_ZDOTDIR"); got != "/tmp/user-zdotdir" {
		t.Fatalf("original ZDOTDIR = %q", got)
	}
	zdotdirCount := 0
	for _, value := range command.Env {
		if strings.HasPrefix(value, "ZDOTDIR=") {
			zdotdirCount++
		}
	}
	if zdotdirCount != 1 {
		t.Fatalf("ZDOTDIR occurs %d times in child environment", zdotdirCount)
	}
}

func TestCtrlOSchedulesCommandShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	model := newTestModel()
	defer model.Shutdown()
	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlO})
	if command == nil {
		t.Fatal("Ctrl+O did not schedule a command shell handoff")
	}
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for index := len(environment) - 1; index >= 0; index-- {
		if strings.HasPrefix(environment[index], prefix) {
			return strings.TrimPrefix(environment[index], prefix)
		}
	}
	return ""
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestDisplayVersionLabelsDevelopmentBuild(t *testing.T) {
	if got := displayVersion("dev"); got != "dev build" {
		t.Fatalf("displayVersion(dev) = %q", got)
	}
	if got := displayVersion("v1.2.3"); got != "v1.2.3" {
		t.Fatalf("displayVersion(v1.2.3) = %q", got)
	}
}

type fakePortChecker struct {
	details map[int]*config.PortInfo
}

func (f fakePortChecker) CheckPort(portNumber int) (*config.PortInfo, error) {
	return f.details[portNumber], nil
}

func (f fakePortChecker) CheckPorts([]int) (map[int]*config.PortInfo, error) {
	return f.details, nil
}

func newTestModel() *Model {
	return NewModel(&config.Config{
		Project: "Test Project",
		Services: map[string]config.Service{
			"api":    {Command: "exit 0", Dir: ".", Shell: "sh", Ports: []int{8080}, Tags: []string{"backend"}},
			"worker": {Command: "exit 0", Dir: ".", Shell: "sh", DependsOn: []string{"api"}, Tags: []string{"jobs"}},
		},
	}, "test")
}

func pressKey(model *Model, character rune) {
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}})
}

func clickRenderedText(t *testing.T, model *Model, label string) tea.Cmd {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(model.View()), "\n") {
		if index := strings.Index(line, label); index >= 0 {
			x := lipgloss.Width(line[:index])
			_, command := model.handleMouseMsg(tea.MouseMsg{
				X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
			})
			return command
		}
	}
	t.Fatalf("visible control %q not found:\n%s", label, ansi.Strip(model.View()))
	return nil
}
