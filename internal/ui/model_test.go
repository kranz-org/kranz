package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
	usersettings "github.com/kranz-org/kranz/internal/settings"
)

func TestEnterDoesNotControlServiceLifecycle(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	serviceInstance := model.FocusedService()

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if command != nil {
		t.Fatal("Enter scheduled a lifecycle operation")
	}
	if serviceInstance.Status() != config.StatusStopped {
		t.Fatalf("Enter changed status to %s", serviceInstance.Status())
	}
}

func TestSpaceSelectsServicesAndSTogglesSelection(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model.moveFocus(1)
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if len(model.selected) != 2 {
		t.Fatalf("selected service count = %d, want 2", len(model.selected))
	}

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if command == nil {
		t.Fatal("s did not schedule selection start")
	}
	rawMessage := command()
	message, ok := rawMessage.(operationResultMsg)
	if !ok {
		t.Fatalf("command returned %T", rawMessage)
	}
	if message.kind != operationStartSet || message.err != nil {
		t.Fatalf("selection result = kind %q, error %v", message.kind, message.err)
	}
}

func TestSStopsTargetsWhenAllAreActive(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	for _, svc := range model.allServices {
		svc.SetStatus(config.StatusRunning)
		model.selected[svc.Name] = true
	}

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	message := command().(operationResultMsg)
	if message.kind != operationStopSet || message.err != nil {
		t.Fatalf("selection result = kind %q, error %v", message.kind, message.err)
	}
	for _, svc := range model.allServices {
		if svc.Status() != config.StatusStopped {
			t.Errorf("service %s status = %s", svc.Name, svc.Status())
		}
	}
}

func TestASelectsAllAndClearsAll(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	pressKey(model, 'a')
	if len(model.selected) != len(model.allServices) {
		t.Fatalf("a selected %d services, want %d", len(model.selected), len(model.allServices))
	}
	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if command == nil {
		t.Fatal("s did not start the all-services selection")
	}
	message := command().(operationResultMsg)
	if message.kind != operationStartSet || message.err != nil {
		t.Fatalf("selected-all start = kind %q, error %v", message.kind, message.err)
	}
	_, _ = model.Update(message)

	pressKey(model, 'a')
	if len(model.selected) != 0 {
		t.Fatalf("second a left selected services: %v", model.selected)
	}
}

func TestASelectsEveryServiceEvenInTagsMode(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.listMode = listTags

	pressKey(model, 'a')
	targets := model.selectedTargetNames()
	if len(targets) != len(model.allServices) {
		t.Fatalf("tag-mode a targets = %v, want every service", targets)
	}
	if label := model.selectedTargetLabel(targets); label != "2 selected services" {
		t.Fatalf("tag-mode all-selection label = %q", label)
	}
}

func TestExternalPortConflictOffersVerifiedStopAction(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 32, true
	model.operationID = 7
	conflict := &service.PortConflictError{
		Service: "api", Port: 8080, PID: 4242, Process: "outside", Command: "outside --serve", External: true,
	}
	_, _ = model.Update(operationResultMsg{id: 7, target: "selection", err: conflict})
	if model.mode != ModePortConflict || !model.conflictExternal || model.conflictService != "api" {
		t.Fatalf("conflict state = mode %v external %v service %q", model.mode, model.conflictExternal, model.conflictService)
	}
	plain := ansi.Strip(model.renderPortConflictView())
	for _, expected := range []string{"external process", "PID: 4242", "outside --serve", "[k] Stop this external process and retry"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("port conflict modal does not contain %q:\n%s", expected, plain)
		}
	}
	model.portChecker = fakePortChecker{details: map[int]*config.PortInfo{8080: {Port: 8080, PID: 4242}}}
	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if command == nil {
		t.Fatal("k did not schedule a verified external-process stop")
	}
}

func TestPortReleaseRefusesChangedOwner(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.portChecker = fakePortChecker{details: map[int]*config.PortInfo{8080: {Port: 8080, PID: 5252}}}
	message := model.releaseExternalPort(8080, 4242)().(releasePortResultMsg)
	if message.err == nil || !strings.Contains(message.err.Error(), "owner changed") {
		t.Fatalf("changed-owner result = %v", message.err)
	}
}

func TestThemeOverridePrecedenceAndPersistence(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	settingsPath := filepath.Join(t.TempDir(), "settings.yaml")
	model := NewModelWithOptions(&config.Config{
		Project: "Theme", UI: config.UIConfig{Theme: "nord", Accent: "#88C0D0"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{
		Settings: usersettings.Settings{Theme: "dracula", Accent: "#FF00FF"}, SettingsPath: settingsPath,
	})
	defer model.Shutdown()
	if model.activeTheme.Name != "dracula" || model.activeTheme.Accent != "#FF00FF" {
		t.Fatalf("resolved theme = %#v", model.activeTheme)
	}

	model.openThemePicker()
	model.themeCursor = 0
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
	saved, err := usersettings.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Theme != "kranz" || saved.Accent != "#FF00FF" {
		t.Fatalf("saved override = %#v", saved)
	}
}

func TestUserThemeUsesItsOwnAccentInsteadOfProjectAccent(t *testing.T) {
	themeName, accent, background, colorMode := effectiveAppearance(
		config.UIConfig{Theme: "ocean", Accent: "#31C5F4"},
		usersettings.Settings{Theme: "dracula"},
	)
	if themeName != "dracula" || accent != "" || background != backgroundTerminal || colorMode != colorModeAuto {
		t.Fatalf("user theme appearance = %q/%q/%q/%q", themeName, accent, background, colorMode)
	}
	theme, err := ApplyTheme(themeName, accent)
	if err != nil {
		t.Fatal(err)
	}
	if theme.Accent != "#BD93F9" {
		t.Fatalf("Dracula accent = %q, want #BD93F9", theme.Accent)
	}
}

func TestThemePickerAAppliesProjectAccentToSelectedTheme(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	model := NewModelWithOptions(&config.Config{
		Project: "Branded", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{Settings: usersettings.Settings{Theme: "dracula"}})
	defer model.Shutdown()
	model.openThemePicker()
	for index, name := range ThemeNames() {
		if name == "github-light" {
			model.themeCursor = index
			break
		}
	}
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !model.themeProjectAccent || model.activeTheme.Accent != "#2AB630" {
		t.Fatalf("project accent was not previewed: toggle=%v theme=%q", model.themeProjectAccent, model.activeTheme.Accent)
	}
	if model.activeTheme.Name != "github-light" {
		t.Fatalf("selected theme changed to %q", model.activeTheme.Name)
	}
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if model.userSettings.Accent != "#2AB630" || model.userSettings.Theme != "github-light" {
		t.Fatalf("saved picker state = %#v", model.userSettings)
	}
}

func TestThemePickerUsesClearProjectAndAccentToggles(t *testing.T) {
	model := NewModel(&config.Config{
		Project: "Branded", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()
	if !model.themeUseProject || !model.themeProjectAccent {
		t.Fatalf("initial picker modes = project %v accent %v", model.themeUseProject, model.themeProjectAccent)
	}
	pressKey(model, 'p')
	if model.themeUseProject {
		t.Fatal("p did not switch to selected theme")
	}
	pressKey(model, 'p')
	if !model.themeUseProject {
		t.Fatal("p did not switch back to project theme")
	}
	pressKey(model, 'a')
	if model.themeProjectAccent {
		t.Fatal("a did not switch to the theme accent")
	}
	plain := ansi.Strip(model.renderThemeView())
	for _, expected := range []string{
		"Theme: PROJECT · forest", "Accent: THEME DEFAULT", "Background: TERMINAL · inherited", "Mode: AUTO · Dark detected",
		"[p] Theme: Project / Selected", "[a] Accent: Project / Theme default", "[b] Background: Terminal / Theme",
		"[m] Mode: Auto / Dark / Light", "[Enter] Save globally", "[c] Save to project",
	} {
		if !strings.Contains(plain, expected) {
			t.Errorf("theme picker does not explain %q:\n%s", expected, plain)
		}
	}
}

func TestBackgroundSourceIsIndependentAndUserOverrideWins(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	darkTerminal := true
	project := config.UIConfig{Theme: "github-light", Accent: "#0969DA", Background: "theme", ColorMode: "dark"}
	model := NewModelWithOptions(&config.Config{
		Project: "Exact", UI: project,
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{DarkBackground: &darkTerminal})
	defer model.Shutdown()
	if relativeLuminance(mustParseColor(t, model.activeTheme.Background)) >= 0.2 || model.activeTheme.TerminalCanvas {
		t.Fatalf("project dark painted background = %#v", model.activeTheme)
	}

	model.userSettings.Background = backgroundTerminal
	if err := model.applyEffectiveAppearance(); err != nil {
		t.Fatal(err)
	}
	if relativeLuminance(mustParseColor(t, model.activeTheme.Background)) >= 0.2 {
		t.Fatalf("terminal override did not produce a dark canvas: %s", model.activeTheme.Background)
	}
	if model.activeTheme.Accent != "#0969DA" {
		t.Fatalf("background override changed the independent accent: %s", model.activeTheme.Accent)
	}
}

func TestThemePickerPersistsBackgroundOverrideAgainstProject(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.yaml")
	model := NewModelWithOptions(&config.Config{
		Project: "Exact", UI: config.UIConfig{Theme: "forest", Background: "theme"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{SettingsPath: settingsPath})
	defer model.Shutdown()
	model.openThemePicker()
	if model.themeBackground != backgroundTheme {
		t.Fatalf("picker background = %q, want project theme source", model.themeBackground)
	}
	pressKey(model, 'b')
	if model.themeBackground != backgroundTerminal {
		t.Fatalf("b background = %q, want terminal", model.themeBackground)
	}
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
	saved, err := usersettings.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Background != backgroundTerminal {
		t.Fatalf("saved background override = %#v", saved)
	}
}

func TestPaintedCreamThemeSupportsAutoDarkAndForcedLight(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	darkTerminal := true
	model := NewModelWithOptions(&config.Config{
		Project: "Cream", UI: config.UIConfig{Theme: "cream", Background: "theme", ColorMode: "auto"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{DarkBackground: &darkTerminal})
	defer model.Shutdown()
	if model.activeTheme.TerminalCanvas || relativeLuminance(mustParseColor(t, model.activeTheme.Background)) >= 0.2 {
		t.Fatalf("automatic cream dark variant = %#v", model.activeTheme)
	}

	model.openThemePicker()
	pressKey(model, 'm') // auto -> dark
	pressKey(model, 'm') // dark -> light
	if model.themeColorMode != colorModeLight || relativeLuminance(mustParseColor(t, model.activeTheme.Background)) < 0.7 {
		t.Fatalf("forced cream light variant = %q/%#v", model.themeColorMode, model.activeTheme)
	}
	pressKey(model, 'm') // light -> auto
	if model.themeColorMode != colorModeAuto || relativeLuminance(mustParseColor(t, model.activeTheme.Background)) >= 0.2 {
		t.Fatalf("cream auto cycle = %q/%#v", model.themeColorMode, model.activeTheme)
	}
}

func TestGlobalColorModeOverridePersistsIndependently(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.yaml")
	darkTerminal := true
	model := NewModelWithOptions(&config.Config{
		Project: "Mode", UI: config.UIConfig{Theme: "cream", Background: "theme", ColorMode: "dark"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{
		Settings:       usersettings.Settings{ColorMode: "light"},
		SettingsPath:   settingsPath,
		DarkBackground: &darkTerminal,
	})
	defer model.Shutdown()
	if relativeLuminance(mustParseColor(t, model.activeTheme.Background)) < 0.7 {
		t.Fatalf("global light override was not applied: %#v", model.activeTheme)
	}
	model.openThemePicker()
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
	saved, err := usersettings.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ColorMode != colorModeLight {
		t.Fatalf("global color mode = %#v", saved)
	}
}

func TestThemePickerKeepsAllControlsVisibleAtTwentyFourRows(t *testing.T) {
	model := NewModel(&config.Config{
		Project: "Compact", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	model.settingsPath = "/tmp/settings.yaml"
	model.configPaths = []string{"/tmp/kranz.yaml"}
	model.openThemePicker()

	plain := ansi.Strip(model.renderThemeView())
	for _, expected := range []string{"[p] Theme: Project / Selected", "[a] Accent: Project / Theme default", "[b] Background: Terminal / Theme", "[m] Mode: Auto / Dark / Light", "[Enter] Save globally", "[c] Save to project", "[Esc] Cancel", "Global: /tmp/settings.yaml", "Project: /tmp/kranz.yaml"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("24-row theme picker clipped %q:\n%s", expected, plain)
		}
	}
}

func TestReleasePortResultReportsErrorsWithoutRetry(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.mode = ModePortConflict
	_, command := model.Update(releasePortResultMsg{port: 8080, pid: 42, err: errors.New("denied")})
	if command != nil || model.mode != ModePortConflict {
		t.Fatalf("failed release changed mode/command: %v/%v", model.mode, command)
	}
}

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
	clickRenderedText(t, model, "[ ] backend")
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
	clickRenderedText(t, model, "[Esc] clear")
	if model.mode != ModeNormal {
		t.Fatal("clicking search cancel did not return to the dashboard")
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

func TestMouseControlsCompleteThemePicker(t *testing.T) {
	model := NewModel(&config.Config{
		Project: "Branded", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()

	clickRenderedText(t, model, "GitHub Light")
	if model.themeUseProject || model.activeTheme.Name != "github-light" {
		t.Fatalf("theme row click = project %v / %s", model.themeUseProject, model.activeTheme.Name)
	}
	clickRenderedText(t, model, "[p] Theme: Project / Selected")
	if !model.themeUseProject || model.activeTheme.Name != "forest" {
		t.Fatalf("project toggle click = project %v / %s", model.themeUseProject, model.activeTheme.Name)
	}
	clickRenderedText(t, model, "[a] Accent: Project / Theme default")
	if model.themeProjectAccent {
		t.Fatal("accent toggle click did not select the theme default")
	}
	clickRenderedText(t, model, "[b] Background: Terminal / Theme")
	if model.themeBackground != backgroundTheme {
		t.Fatal("background toggle click did not select a painted theme background")
	}
	clickRenderedText(t, model, "[m] Mode: Auto / Dark / Light")
	if model.themeColorMode != colorModeDark {
		t.Fatal("mode toggle click did not select the dark variant")
	}
	clickRenderedText(t, model, "[Enter] Save globally")
	if model.mode != ModeNormal || model.userSettings.Theme != "" || model.userSettings.Accent != "theme" || model.userSettings.Background != "theme" || model.userSettings.ColorMode != "dark" {
		t.Fatalf("theme save click left mode/settings %v/%#v", model.mode, model.userSettings)
	}
}

func TestThemePickerSavesAppearanceToProjectConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kranz.yaml")
	data := "project: Project Theme\nui:\n  theme: forest\n  accent: '#2AB630'\n  background: terminal\nservices:\n  app:\n    command: exit 0\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(t.TempDir(), "settings.yaml")
	model := NewModelWithOptions(cfg, "test", ModelOptions{
		Settings:     usersettings.Settings{Theme: "dracula", Accent: "theme", Background: "theme"},
		SettingsPath: settingsPath,
	})
	defer model.Shutdown()
	model.openThemePicker()
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if model.mode != ModeNormal {
		t.Fatal("successful project save did not close the theme picker")
	}

	savedConfig, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := config.UIConfig{Theme: "dracula", Background: "theme", ColorMode: "auto"}
	if savedConfig.UI != want {
		t.Fatalf("project appearance = %#v, want %#v", savedConfig.UI, want)
	}
	savedSettings, err := usersettings.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if savedSettings != (usersettings.Settings{}) {
		t.Fatalf("user overrides were not cleared: %#v", savedSettings)
	}
}

func TestLightTerminalUsesCohesiveAdaptiveCanvas(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()

	darkBackground := false
	model := NewModelWithOptions(&config.Config{
		Project: "MyClass", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
		Services: map[string]config.Service{"im-core": {Command: "npm run dev", Description: "Messenger API"}},
	}, "test", ModelOptions{DarkBackground: &darkBackground})
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true

	model.openThemePicker()
	if model.terminalDark || model.themePickerBackgroundLabel() != "TERMINAL · inherited" || model.themePickerColorModeLabel() != "AUTO · Light detected" {
		t.Fatalf("terminal mode = %v/%q/%q", model.terminalDark, model.themePickerBackgroundLabel(), model.themePickerColorModeLabel())
	}
	model.cancelThemePicker()
	if relativeLuminance(mustParseColor(t, model.activeTheme.Background)) < 0.7 {
		t.Fatalf("canvas did not adapt to light terminal: %#v", model.activeTheme)
	}
	if model.activeTheme.Background != model.activeTheme.Surface {
		t.Fatalf("adaptive canvas/panel split = %s/%s", model.activeTheme.Background, model.activeTheme.Surface)
	}
	_, appUsesTerminal := AppStyle.GetBackground().(lipgloss.NoColor)
	_, panelUsesTerminal := PanelStyle.GetBackground().(lipgloss.NoColor)
	if !model.activeTheme.TerminalCanvas || !appUsesTerminal || !panelUsesTerminal {
		t.Fatalf("terminal-owned canvas is still painted: theme=%v app=%#v panel=%#v",
			model.activeTheme.TerminalCanvas, AppStyle.GetBackground(), PanelStyle.GetBackground())
	}
	rendered := model.View()
	if height := lipgloss.Height(rendered); height != model.height {
		t.Fatalf("adaptive view height = %d, want %d", height, model.height)
	}
}

func TestExactThemeNestedStylesRestoreTheCanvasBackground(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()

	for _, testCase := range []struct {
		name         string
		theme        string
		darkTerminal bool
	}{
		{name: "dark ocean", theme: "ocean", darkTerminal: true},
		{name: "light GitHub", theme: "github-light", darkTerminal: true},
		{name: "warm cream", theme: "cream", darkTerminal: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			model := NewModelWithOptions(&config.Config{
				Project: "Uniform", UI: config.UIConfig{Theme: testCase.theme, Background: backgroundTheme},
				Services: map[string]config.Service{"api": {Command: "exit 0", Tags: []string{"backend"}}},
			}, "test", ModelOptions{DarkBackground: &testCase.darkTerminal})
			defer model.Shutdown()
			model.width, model.height, model.ready = 100, 28, true

			assertFrameRestoresCanvasBackground(t, model.View())
			model.openThemePicker()
			assertFrameRestoresCanvasBackground(t, model.View())
		})
	}
}

func assertFrameRestoresCanvasBackground(t *testing.T, frame string) {
	t.Helper()
	backgroundPrefix := terminalStylePrefix(lipgloss.NewStyle().Background(ColorBackground))
	if backgroundPrefix == "" {
		t.Fatal("true-color background style did not produce an ANSI prefix")
	}
	const reset = "\x1b[0m"
	if !strings.HasSuffix(frame, reset) {
		t.Fatal("frame does not end by resetting terminal styles")
	}
	for offset := 0; ; {
		relative := strings.Index(frame[offset:], reset)
		if relative < 0 {
			break
		}
		resetEnd := offset + relative + len(reset)
		if resetEnd < len(frame) && frame[resetEnd] != '\n' && !strings.HasPrefix(frame[resetEnd:], backgroundPrefix) {
			t.Fatalf("nested reset at byte %d exposes terminal background; next bytes %q", resetEnd-len(reset), frame[resetEnd:min(len(frame), resetEnd+len(backgroundPrefix)+12)])
		}
		offset = resetEnd
	}
}

func TestFocusingServiceClearsUnreadLogs(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	first := model.services[0]
	second := model.services[1]
	first.AppendLog("first unread")
	second.AppendLog("second unread")

	model.moveFocus(1)
	if first.NewLogCount() != 0 {
		t.Fatalf("previously focused service has %d unread logs", first.NewLogCount())
	}
	if second.NewLogCount() != 0 {
		t.Fatalf("newly focused service has %d unread logs", second.NewLogCount())
	}

	second.AppendLog("visible while focused")
	model.refreshServices()
	if second.NewLogCount() != 0 {
		t.Fatalf("focused service accumulated %d unread logs", second.NewLogCount())
	}
}

func TestServiceDetailsUseAsyncPortInspection(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.FocusedService().Config.HealthCheck = &config.HealthCheckConfig{
		Readiness: &config.CheckConfig{Type: config.CheckHTTP, URL: "http://127.0.0.1:8080/ready"},
		Liveness:  &config.CheckConfig{Type: config.CheckTCP, Port: 8080},
	}
	model.portChecker = fakePortChecker{details: map[int]*config.PortInfo{
		8080: {Port: 8080, Address: "127.0.0.1", Protocol: "tcp", PID: 4321, Process: "test-api"},
	}}

	command := model.scanFocusedPorts(true)
	if command == nil {
		t.Fatal("port scan was not scheduled")
	}
	message := command().(portDetailsMsg)
	_, _ = model.Update(message)
	plain := ansi.Strip(model.renderServiceDetails(model.FocusedService(), 72, 24))
	for _, expected := range []string{"tcp://127.0.0.1:8080", "listening", "test-api", "PID 4321", "backend", "http://127.0.0.1:8080/ready", "tcp://localhost:8080", "COMMAND exit 0"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("service details do not contain %q:\n%s", expected, plain)
		}
	}
}

func TestListeningPortUsesConciseOwnershipParameter(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		info           *config.PortInfo
		managedService string
		want           string
	}{
		{name: "managed", info: &config.PortInfo{Process: "node", PID: 4321}, managedService: "api", want: "owner: kranz"},
		{name: "external", info: &config.PortInfo{Process: "postgres", PID: 5432}, want: "owner: external"},
		{name: "unknown", info: &config.PortInfo{Process: "listener"}, want: "owner: unknown"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plain := ansi.Strip(strings.Join(renderListeningPort("", testCase.info, testCase.managedService), "\n"))
			if !strings.Contains(plain, testCase.want) {
				t.Fatalf("port ownership = %q, want %q", plain, testCase.want)
			}
			if strings.Contains(plain, "Kranz ·") || strings.Contains(plain, "· api") {
				t.Fatalf("port ownership repeats product/service context: %q", plain)
			}
		})
	}
}

func TestServiceDetailsScrollWhenContentOverflows(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	svc := model.FocusedService()
	svc.Config.Ports = []int{8001, 8002, 8003, 8004, 8005, 8006, 8007, 8008, 8009, 8010, 8011, 8012}
	model.portService = svc.Name
	model.portChecked = time.Now()
	model.width, model.height, model.ready = 80, 24, true

	_, detailHeight := model.serviceColumnLayout(model.height - 2)
	initial := ansi.Strip(model.renderServiceDetails(svc, 48, detailHeight))
	if strings.Contains(initial, "COMMAND") {
		t.Fatalf("command should initially be below the viewport:\n%s", initial)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyDown})
	if model.detailOffset != 1 {
		t.Fatalf("] changed detail offset to %d, want 1", model.detailOffset)
	}
	for range 20 {
		model.movePanelCursor(1)
	}
	scrolled := ansi.Strip(model.renderServiceDetails(svc, 48, detailHeight))
	if !strings.Contains(scrolled, "COMMAND") || !strings.Contains(scrolled, "↑/↓") {
		t.Fatalf("scrolled details do not expose the end and scroll hint:\n%s", scrolled)
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
	if details := ansi.Strip(strings.Join(model.serviceDetailLines(svc), "\n")); !strings.Contains(details, "Queued") || !strings.Contains(details, "Waiting for dependencies: database") {
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

func TestShiftThreePinsLogsAboveFocusedLogs(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 28, true
	api := model.FocusedService()
	api.AppendLog("api log remains visible")

	pressKey(model, '#')
	if model.pinnedLog != "api" || model.PinnedService() != api {
		t.Fatalf("pinned service = %q / %v", model.pinnedLog, model.PinnedService())
	}
	model.moveFocus(1)
	worker := model.FocusedService()
	worker.AppendLog("hidden worker line")
	worker.AppendLog("WORKER matched line")
	if err := model.logSearcher.SetPattern("WORKER"); err != nil {
		t.Fatal(err)
	}
	model.searchMode = searchFilter

	rendered := model.renderLogColumn(64, model.height-2)
	plain := ansi.Strip(rendered)
	for _, expected := range []string{"PINNED LOGS", "api log remains visible", "[3] LOGS", "worker", "WORKER matched line"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("split logs do not contain %q:\n%s", expected, plain)
		}
	}
	if strings.Contains(plain, "hidden worker line") {
		t.Fatalf("focused log filter did not hide a non-match:\n%s", plain)
	}
	if height := lipgloss.Height(rendered); height != model.height-2 {
		t.Fatalf("split log height = %d, want %d", height, model.height-2)
	}
	if model.currentLogPanelHeight() != (model.height-2)-(model.height-2)/2 {
		t.Fatalf("current log panel height = %d", model.currentLogPanelHeight())
	}
}

func TestThreeSwitchesAndScrollsPinnedLogsIndependently(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 100, 28, true
	for index := range 40 {
		model.FocusedService().AppendLog(fmt.Sprintf("pinned line %d", index))
	}
	pressKey(model, '#')
	model.moveFocus(1)
	for index := range 40 {
		model.FocusedService().AppendLog(fmt.Sprintf("current line %d", index))
	}

	pressKey(model, '3')
	if model.panelFocus != panelLogs {
		t.Fatalf("first 3 focused panel %v, want current logs", model.panelFocus)
	}
	pressKey(model, '3')
	if model.panelFocus != panelPinnedLogs {
		t.Fatalf("second 3 focused panel %v, want pinned logs", model.panelFocus)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyUp})
	if model.pinnedOffset == 0 || model.pinnedFollow || model.logOffset != 0 || !model.followMode {
		t.Fatalf("pinned/current viewports = %d/%v and %d/%v", model.pinnedOffset, model.pinnedFollow, model.logOffset, model.followMode)
	}
	plain := ansi.Strip(model.renderLogColumn(64, model.height-2))
	if !strings.Contains(plain, "PINNED LOGS") || !strings.Contains(plain, "BROWSING") {
		t.Fatalf("focused pinned viewport does not expose its browsing state:\n%s", plain)
	}
	pressKey(model, '3')
	if model.panelFocus != panelLogs {
		t.Fatalf("third 3 focused panel %v, want current logs", model.panelFocus)
	}

	model.pinnedOffset, model.pinnedAnchor, model.pinnedFollow = 0, 0, true
	rightX := model.dashboardLeftWidth() + 1
	_, _ = model.handleMouseMsg(tea.MouseMsg{X: rightX, Y: 2, Button: tea.MouseButtonWheelUp})
	if model.panelFocus != panelPinnedLogs || model.pinnedOffset == 0 {
		t.Fatalf("wheel over pinned logs focused %v at offset %d", model.panelFocus, model.pinnedOffset)
	}
	_, _ = model.handleMouseMsg(tea.MouseMsg{X: rightX, Y: model.height - 3, Button: tea.MouseButtonWheelUp})
	if model.panelFocus != panelLogs || model.logOffset == 0 {
		t.Fatalf("wheel over current logs focused %v at offset %d", model.panelFocus, model.logOffset)
	}
}

func TestShiftThreeReplacesAndUnpinsPinnedService(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	pressKey(model, '#')
	model.moveFocus(1)
	pressKey(model, '#')
	if model.pinnedLog != "worker" {
		t.Fatalf("replacement pinned service = %q", model.pinnedLog)
	}
	pressKey(model, '#')
	if model.pinnedLog != "" || model.PinnedService() != nil {
		t.Fatalf("pinned service was not cleared: %q", model.pinnedLog)
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
	if listHeight != 16 || detailHeight != 6 {
		t.Fatalf("25-item, 22-row column split = %d/%d, want usable 16/6", listHeight, detailHeight)
	}
}

func TestTagsPanelSelectsLifecycleTargets(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.selected["worker"] = true
	servicesTitle := ansi.Strip(model.renderServicePanel(40, 8))
	if !strings.Contains(servicesTitle, "1 → Tags") {
		t.Fatalf("services panel does not explain tag switching:\n%s", servicesTitle)
	}

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if model.listMode != listTags || model.panelFocus != panelServices {
		t.Fatalf("tag view state = mode %v panel %v", model.listMode, model.panelFocus)
	}
	if len(model.selected) != 0 || len(model.selectedTags) != 1 || model.selectedTags[0] != "backend" {
		t.Fatalf("selection = services %v tags %v", model.selected, model.selectedTags)
	}
	if targets := model.selectedTargetNames(); len(targets) != 1 || targets[0] != "api" {
		t.Fatalf("backend tag targets = %v", targets)
	}
	plain := ansi.Strip(model.renderServicePanel(40, 8))
	if !strings.Contains(plain, "[1] TAGS") || !strings.Contains(plain, "1 → Services") || !strings.Contains(plain, "backend (1)") {
		t.Fatalf("tag panel is incomplete:\n%s", plain)
	}
}

func TestFooterPrioritizesRegexHint(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	footer := ansi.Strip(model.contextMessage())
	if !strings.Contains(footer, "[/] regex filter") {
		t.Fatalf("footer does not expose regex grep: %q", footer)
	}
	for _, numberHint := range []string{"[1]", "[2]", "[3]"} {
		if strings.Contains(footer, numberHint) {
			t.Fatalf("footer still contains panel hint %q: %q", numberHint, footer)
		}
	}
}

func TestHealthTargetsStartAtFirstColumn(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()

	for _, testCase := range []struct {
		check *config.CheckConfig
		want  string
	}{
		{check: &config.CheckConfig{Type: config.CheckHTTP, URL: "http://localhost:3801/healthz"}, want: "http://localhost:3801/healthz"},
		{check: &config.CheckConfig{Type: config.CheckTCP, Port: 3801}, want: "tcp://localhost:3801"},
	} {
		lines := model.healthDetailLines("READINESS", testCase.check, "waiting")
		if got := ansi.Strip(lines[1]); got != testCase.want {
			t.Errorf("health target line = %q, want %q", got, testCase.want)
		}
	}
}

func TestStopInterruptsReadinessGatedStart(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {
			Command: "sleep 60", Dir: ".", Shell: "sh",
			HealthCheck: &config.HealthCheckConfig{Readiness: &config.CheckConfig{
				Type: config.CheckCommand, Command: "exit 1", Interval: time.Hour, Timeout: time.Second,
			}},
		},
	}}, "test")
	defer model.Shutdown()

	_, startCommand := model.toggleSelectedServices()
	startResult := make(chan operationResultMsg, 1)
	go func() { startResult <- startCommand().(operationResultMsg) }()
	waitForServiceStatus(t, model.FocusedService(), config.StatusRunning)

	_, stopCommand := model.toggleSelectedServices()
	if stopCommand == nil {
		t.Fatal("s was blocked while start waited for readiness")
	}
	stopMessage := stopCommand().(operationResultMsg)
	_, _ = model.Update(stopMessage)
	if model.FocusedService().Status() != config.StatusStopped {
		t.Fatalf("service status = %s after interrupted stop", model.FocusedService().Status())
	}
	select {
	case stale := <-startResult:
		_, _ = model.Update(stale)
	case <-time.After(time.Second):
		t.Fatal("canceled start did not return promptly")
	}
}

func TestShiftSForceStartsOnlySelectedService(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"database": {Command: "sleep 60", Dir: ".", Shell: "sh"},
		"api":      {Command: "sleep 60", Dir: ".", Shell: "sh", DependsOn: []string{"database"}},
	}}, "test")
	defer model.Shutdown()
	model.selected["api"] = true

	_, command := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if command == nil || model.operationKind != operationForceStart {
		t.Fatal("Shift+S did not schedule force start")
	}
	message := command().(operationResultMsg)
	_, _ = model.Update(message)
	api, _ := model.manager.GetService("api")
	database, _ := model.manager.GetService("database")
	if api.Status() != config.StatusRunning || database.Status() != config.StatusStopped {
		t.Fatalf("force start statuses: api=%s database=%s", api.Status(), database.Status())
	}
	if !strings.Contains(model.toastMessage, "without dependencies") {
		t.Fatalf("force start notification = %q", model.toastMessage)
	}
}

func TestShiftSOverridesQueuedDependencyStart(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"server": {Command: "sleep 60", Dir: ".", Shell: "sh", ReadyLogLine: "NEVER"},
		"api": {
			Command: "sleep 60", Dir: ".", Shell: "sh", DependsOn: []string{"server"},
			DependencyConditions: map[string]config.DependencyConfig{
				"server": {Condition: config.DependencyLogReady},
			},
		},
	}}, "test")
	defer model.Shutdown()
	model.selected["api"] = true

	_, queuedCommand := model.toggleSelectedServices()
	queuedResult := make(chan operationResultMsg, 1)
	go func() { queuedResult <- queuedCommand().(operationResultMsg) }()
	api, _ := model.manager.GetService("api")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && (api.Status() != config.StatusStopped || !api.DesiredRunning()) {
		time.Sleep(10 * time.Millisecond)
	}
	if api.Status() != config.StatusStopped || !api.DesiredRunning() {
		t.Fatalf("api did not enter queued state: status=%s desired=%v", api.Status(), api.DesiredRunning())
	}

	_, forceCommand := model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if forceCommand == nil || model.operationKind != operationForceStart {
		t.Fatal("Shift+S did not replace the queued start")
	}
	_, _ = model.Update(forceCommand().(operationResultMsg))
	if api.Status() != config.StatusRunning {
		t.Fatalf("force-started api status = %s", api.Status())
	}
	select {
	case stale := <-queuedResult:
		_, _ = model.Update(stale)
	case <-time.After(time.Second):
		t.Fatal("overridden dependency start did not cancel promptly")
	}
}

func TestMouseCanForceStartFocusedService(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"api": {Command: "sleep 60", Dir: ".", Shell: "sh"},
	}}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true

	command := clickRenderedText(t, model, "Force: S")
	if command == nil || model.operationKind != operationForceStart {
		t.Fatal("force-start button did not schedule the operation")
	}
	_, _ = model.Update(command().(operationResultMsg))
	if model.FocusedService().Status() != config.StatusRunning {
		t.Fatalf("focused service status = %s", model.FocusedService().Status())
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

func TestDetailsShowLifecycleConfiguration(t *testing.T) {
	model := NewModel(&config.Config{Project: "Test", Services: map[string]config.Service{
		"db": {Command: "sleep 60", ReadyLogLine: "READY"},
		"api": {
			Command: "sleep 60", DependsOn: []string{"db"},
			DependencyConditions: map[string]config.DependencyConfig{"db": {Condition: config.DependencyLogReady}},
			Availability:         config.AvailabilityConfig{Restart: "on_failure", Backoff: 2 * time.Second, MaxRestarts: 3, ExitOnSkipped: true},
			Shutdown:             config.ShutdownConfig{Signal: 2, Timeout: 5 * time.Second, ParentOnly: true},
			EnvFiles:             []string{"api.env"}, SuccessExitCodes: []int{7}, Disabled: true,
		},
	}}, "test")
	defer model.Shutdown()
	model.focused = 0
	for index, svc := range model.services {
		if svc.Name == "api" {
			model.focused = index
		}
	}
	plain := ansi.Strip(strings.Join(model.serviceDetailLines(model.FocusedService()), "\n"))
	for _, expected := range []string{
		"db · process_log_ready", "RECOVERY\n  ↳ restart on_failure\n  ↳ backoff 2s\n  ↳ limit 3",
		"SHUTDOWN\n  ↳ signal 2\n  ↳ timeout 5s\n  ↳ target parent only",
		"ENV FILES api.env", "SUCCESS 0, 7", "DISABLED manual start only",
	} {
		if !strings.Contains(plain, expected) {
			t.Errorf("details do not contain %q:\n%s", expected, plain)
		}
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

func TestRegexSearchFiltersMatchingLogsByDefault(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	model.FocusedService().AppendLog("request complete")
	model.FocusedService().AppendLog("ERROR database unavailable")

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, character := range "ERROR" {
		_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}})
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if !model.logSearcher.HasPattern() || model.searchMode != searchFilter || model.currentMatch != -1 || model.panelFocus != panelLogs || model.mode != ModeNormal {
		t.Fatalf("search state = pattern %v mode %v match %d panel %v view %v", model.logSearcher.HasPattern(), model.searchMode, model.currentMatch, model.panelFocus, model.mode)
	}
	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 12))
	if !strings.Contains(plain, "ERROR database unavailable") || strings.Contains(plain, "request complete") || !strings.Contains(plain, "FILTER /ERROR/ · 1") {
		t.Fatalf("filter mode rendered unexpected logs:\n%s", plain)
	}
	if !model.followMode || strings.Contains(plain, "PAUSED") || strings.Contains(plain, "BROWSING") {
		t.Fatalf("applying a filter changed follow state:\n%s", plain)
	}
}

func TestRegexTabEnablesHighlightModeWithoutFalsePausedLabel(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 12, true
	for index := range 20 {
		line := fmt.Sprintf("line %02d", index)
		if index == 3 {
			line = "ERROR database unavailable"
		}
		model.FocusedService().AppendLog(line)
	}

	pressKey(model, '/')
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyTab})
	for _, character := range "ERROR" {
		pressKey(model, character)
	}
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})

	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 10))
	if model.searchMode != searchHighlight || model.currentMatch != 3 || !strings.Contains(plain, "HIGHLIGHT /ERROR/ · 1") {
		t.Fatalf("highlight state = mode %v match %d:\n%s", model.searchMode, model.currentMatch, plain)
	}
	if strings.Contains(plain, "PAUSED") || !strings.Contains(plain, "BROWSING") {
		t.Fatalf("match navigation should be labeled BROWSING, not PAUSED:\n%s", plain)
	}
}

func TestManualLogPauseFreezesVisibleTail(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 10, true
	for index := range 10 {
		model.FocusedService().AppendLog(fmt.Sprintf("before %02d", index))
	}
	pressKey(model, 'f')
	model.FocusedService().AppendLog("after pause")

	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 8))
	if !strings.Contains(plain, "PAUSED") || strings.Contains(plain, "after pause") {
		t.Fatalf("manual pause did not freeze the visible log tail:\n%s", plain)
	}
}

func TestLogWrappingKeepsPanelAndDashboardGeometry(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 80, 24, true
	model.panelFocus = panelLogs
	for range 3 {
		model.FocusedService().AppendLog(strings.Repeat("long-log-value ", 40))
	}

	plainRows := model.displayedLogLineCount()
	panel := model.renderLogPanel(model.FocusedService(), 50, 12)
	if lipgloss.Height(panel) != 12 {
		t.Fatalf("unwrapped panel height = %d, want 12", lipgloss.Height(panel))
	}
	pressKey(model, 'w')
	wrappedRows := model.displayedLogLineCount()
	if wrappedRows <= plainRows {
		t.Fatalf("wrapped rows = %d, unwrapped rows = %d", wrappedRows, plainRows)
	}
	panel = model.renderLogPanel(model.FocusedService(), 50, 12)
	if lipgloss.Height(panel) != 12 || !strings.Contains(ansi.Strip(panel), "WRAP") {
		t.Fatalf("wrapped panel geometry/state is invalid: height=%d\n%s", lipgloss.Height(panel), ansi.Strip(panel))
	}
	if height := lipgloss.Height(model.View()); height != model.height {
		t.Fatalf("dashboard grew to %d rows, want %d", height, model.height)
	}
	model.scrollLogs(-1)
	if model.followMode || model.logOffset == 0 {
		t.Fatalf("wrapped log scrolling did not enter browsing mode: follow=%v offset=%d", model.followMode, model.logOffset)
	}
}

func TestLogTimestampToggleUsesCaptureTimeWithoutChangingSearchText(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 90, 24, true
	captured := time.Date(2026, 7, 20, 12, 34, 56, 789000000, time.Local)
	model.FocusedService().AppendLogAt(captured, "request complete")

	pressKey(model, 'i')
	plain := ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 10))
	if !strings.Contains(plain, "TIME") || !strings.Contains(plain, "[12:34:56.789] request complete") {
		t.Fatalf("timestamp mode did not render capture time:\n%s", plain)
	}
	if err := model.logSearcher.SetPattern(`^request complete$`); err != nil {
		t.Fatal(err)
	}
	if matches := model.logSearcher.Search(serviceLogLines(model.FocusedService())); len(matches) != 1 {
		t.Fatalf("timestamp polluted regex source: matches=%v lines=%v", matches, serviceLogLines(model.FocusedService()))
	}

	pressKey(model, 'i')
	plain = ansi.Strip(model.renderLogPanel(model.FocusedService(), 70, 10))
	if strings.Contains(plain, "12:34:56.789") || model.showLogTime {
		t.Fatalf("timestamp toggle did not hide capture time:\n%s", plain)
	}
}

func TestServiceLogCannotClearOrRepositionTheTUI(t *testing.T) {
	model := newTestModel()
	defer model.Shutdown()
	model.width, model.height, model.ready = 90, 24, true
	model.FocusedService().AppendLog("\x1b[2J\x1b[H\x1b[31mserver ready\x1b[0m")

	rendered := model.View()
	if strings.Contains(rendered, "\x1b[2J") || strings.Contains(rendered, "\x1b[H") {
		t.Fatalf("unsafe child-process control sequence reached the TUI: %q", rendered)
	}
	plain := ansi.Strip(rendered)
	for _, expected := range []string{"KRANZ", "SERVICES", "DETAILS", "LOGS", "server ready", "Start"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("sanitized render lost %q:\n%s", expected, plain)
		}
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

func waitForServiceStatus(t *testing.T, svc interface{ Status() config.ServiceStatus }, expected config.ServiceStatus) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if svc.Status() == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("service status = %s, want %s", svc.Status(), expected)
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
