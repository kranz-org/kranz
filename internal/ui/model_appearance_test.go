package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kranz-org/kranz/internal/config"
	usersettings "github.com/kranz-org/kranz/internal/settings"
	"github.com/muesli/termenv"
)

// Tests for appearance resolution and the theme picker.

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
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	saved, err := usersettings.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Theme != "kranz" || saved.Accent != "#FF00FF" {
		t.Fatalf("saved override = %#v", saved)
	}
}

func TestThemePickerEnterAppliesWithoutSaving(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.yaml")
	model := NewModelWithOptions(&config.Config{
		Project: "Session", UI: config.UIConfig{Theme: "forest"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{SettingsPath: settingsPath})
	defer model.Shutdown()

	model.openThemePicker()
	pressKey(model, 'j')
	wantTheme := model.activeTheme.Name
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})

	if model.mode != ModeNormal || model.userSettings.Theme != wantTheme || model.activeTheme.Name != wantTheme {
		t.Fatalf("session appearance = mode %v, active %q, settings %#v", model.mode, model.activeTheme.Name, model.userSettings)
	}
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("Enter wrote global settings: %v", err)
	}

	model.openThemePicker()
	if model.themeUseProject || ThemeNames()[model.themeCursor] != wantTheme {
		t.Fatalf("reopened picker lost session theme: project=%v cursor=%q", model.themeUseProject, ThemeNames()[model.themeCursor])
	}
}

func TestThemeAccentEditorOpensWithoutProjectAccentAndAppliesHexSuffix(t *testing.T) {
	model := NewModel(&config.Config{
		Project: "Custom Accent", UI: config.UIConfig{Theme: "forest"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()
	originalAccent := model.activeTheme.Accent

	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !model.themeColorEditing || model.themeColorInput.Value() != strings.TrimPrefix(originalAccent, "#") {
		t.Fatalf("accent editor = active %v / value %q", model.themeColorEditing, model.themeColorInput.Value())
	}
	plain := ansi.Strip(model.renderThemeView())
	for _, expected := range []string{"Accent: #" + strings.TrimPrefix(originalAccent, "#"), "[Enter] Apply", "[Esc] Cancel", "[a/Shift+A] Accent: Edit color"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("accent editor does not contain %q:\n%s", expected, plain)
		}
	}

	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("#12abefz")})
	if got := model.themeColorInput.Value(); got != "12ABEF" {
		t.Fatalf("sanitized accent input = %q", got)
	}
	candidateStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#12ABEF")).Bold(true)
	accentSetting := model.renderThemePickerAccentSetting()
	// The field carries the colour as its background and the dot repeats it.
	field := lipgloss.NewStyle().
		Foreground(lipgloss.Color(readableTextOn("#12ABEF", model.activeTheme.Text))).
		Background(lipgloss.Color("#12ABEF")).
		Bold(true)
	if !strings.Contains(accentSetting, field.Render("12ABEF")) || !strings.Contains(accentSetting, candidateStyle.Render("●")) {
		t.Fatalf("valid accent candidate is not highlighted: %q", accentSetting)
	}
	if model.activeTheme.Accent != originalAccent {
		t.Fatalf("accent changed before Enter: %q", model.activeTheme.Accent)
	}
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if model.themeColorEditing || model.mode != ModeThemes || model.activeTheme.Accent != "#12ABEF" || model.themeCustomAccent != "#12ABEF" {
		t.Fatalf("applied accent editor = editing %v / mode %v / accent %q / custom %q",
			model.themeColorEditing, model.mode, model.activeTheme.Accent, model.themeCustomAccent)
	}

	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if model.mode != ModeNormal || model.userSettings.Accent != "#12ABEF" {
		t.Fatalf("session accent = mode %v / settings %#v", model.mode, model.userSettings)
	}
}

func TestThemeAccentEditorRejectsIncompleteInputAndEscCancels(t *testing.T) {
	model := NewModel(&config.Config{
		Project: "Project Accent", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()
	originalAccent := model.activeTheme.Accent

	// Terminal columns, not byte offsets: the swatch glyph is multi-byte, so
	// strings.Index would report a shift the user never sees.
	actionColumn := func(setting string) int {
		t.Helper()
		index := strings.Index(setting, "[Enter]")
		if index < 0 {
			t.Fatalf("accent editor actions are missing: %q", setting)
		}
		return lipgloss.Width(setting[:index])
	}

	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	initialSetting := ansi.Strip(model.renderThemePickerAccentSetting())
	initialActionColumn := actionColumn(initialSetting)
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("123")})
	partialSetting := ansi.Strip(model.renderThemePickerAccentSetting())
	if column := actionColumn(partialSetting); column != initialActionColumn {
		t.Fatalf("accent editor actions shifted from column %d to %d: initial %q / partial %q",
			initialActionColumn, column, initialSetting, partialSetting)
	}
	if lipgloss.Width(initialSetting) != lipgloss.Width(partialSetting) {
		t.Fatalf("accent editor row changed width: %d vs %d", lipgloss.Width(initialSetting), lipgloss.Width(partialSetting))
	}
	if !strings.Contains(partialSetting, "○") {
		t.Fatalf("incomplete accent does not reserve the swatch: %q", partialSetting)
	}
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if !model.themeColorEditing || model.themeColorError == "" || model.activeTheme.Accent != originalAccent {
		t.Fatalf("incomplete accent = editing %v / error %q / accent %q", model.themeColorEditing, model.themeColorError, model.activeTheme.Accent)
	}
	if plain := ansi.Strip(model.renderThemeView()); !strings.Contains(plain, "Enter 6 hex digits") {
		t.Fatalf("accent validation is not visible:\n%s", plain)
	}

	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEsc})
	if model.themeColorEditing || model.mode != ModeThemes || model.activeTheme.Accent != originalAccent || model.themeAccentChanged {
		t.Fatalf("cancelled accent editor = editing %v / mode %v / accent %q / changed %v",
			model.themeColorEditing, model.mode, model.activeTheme.Accent, model.themeAccentChanged)
	}
}

func TestThemePickerReloadsSavedAppearanceFromDisk(t *testing.T) {
	tempDir := t.TempDir()
	projectPath := filepath.Join(tempDir, "kranz.yaml")
	projectData := "project: Reloaded\nui:\n  theme: forest\n  accent: '#2AB630'\nservices:\n  app:\n    command: exit 0\n"
	if err := os.WriteFile(projectPath, []byte(projectData), 0o600); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(tempDir, "settings.yaml")
	savedSettings := usersettings.Settings{Theme: "dracula", Background: backgroundTheme, ColorMode: colorModeDark}
	if err := usersettings.Save(settingsPath, savedSettings); err != nil {
		t.Fatal(err)
	}

	model := NewModelWithOptions(&config.Config{
		Project: "Stale", UI: config.UIConfig{Theme: "nord"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{
		Settings:     usersettings.Settings{Theme: "github-light"},
		SettingsPath: settingsPath,
		ConfigPaths:  []string{projectPath},
	})
	defer model.Shutdown()
	model.openThemePicker()
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	if model.mode != ModeThemes || model.cfg.UI.Theme != "forest" || model.userSettings != savedSettings || model.activeTheme.Name != "dracula" {
		t.Fatalf("reloaded appearance = mode %v / project %#v / settings %#v / active %q",
			model.mode, model.cfg.UI, model.userSettings, model.activeTheme.Name)
	}
	if model.themeUseProject || ThemeNames()[model.themeCursor] != "dracula" || model.themeBackgroundSource != themeBackgroundSourceTheme || model.themeColorMode != colorModeDark {
		t.Fatalf("reloaded picker controls = project %v / theme %q / background %q / mode %q",
			model.themeUseProject, ThemeNames()[model.themeCursor], model.themePickerBackground(), model.themeColorMode)
	}
	if model.activeTheme.TerminalCanvas {
		t.Fatal("saved theme background ownership was not restored")
	}
	model.cancelThemePicker()
	if model.activeTheme.Name != "dracula" || model.userSettings != savedSettings {
		t.Fatalf("cancel reverted the reloaded baseline: %q / %#v", model.activeTheme.Name, model.userSettings)
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
	if model.themeAccentSource != themeAccentSourceProject || model.activeTheme.Accent != "#2AB630" {
		t.Fatalf("project accent was not previewed: source=%v theme=%q", model.themeAccentSource, model.activeTheme.Accent)
	}
	if model.activeTheme.Name != "github-light" {
		t.Fatalf("selected theme changed to %q", model.activeTheme.Name)
	}
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if model.mode != ModeNormal || model.activeTheme.Accent != "#2AB630" || model.userSettings.Accent != "#2AB630" || model.userSettings.Theme != "github-light" {
		t.Fatalf("applied picker state = mode %v, active %#v, settings %#v", model.mode, model.activeTheme, model.userSettings)
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
	if !model.themeUseProject || model.themeAccentSource != themeAccentSourceProject {
		t.Fatalf("initial picker modes = project %v accent %v", model.themeUseProject, model.themeAccentSource)
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
	if model.themeAccentSource != themeAccentSourceTheme {
		t.Fatal("a did not switch to the theme accent")
	}
	plain := ansi.Strip(model.renderThemeView())
	for _, expected := range []string{
		"Theme: PROJECT · forest", "Accent: THEME DEFAULT", "Background: TERMINAL · inherited", "Mode: AUTO · Dark detected",
		"[p] Theme: Project / Selected", "[a] Accent: Project / Theme", "[b] Background: Terminal / Theme",
		"[m] Mode: Auto / Dark / Light", "SESSION", "[Enter] Apply", "[r] Reload saved", "SAVE", "[g] Global", "[c] Project",
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
	if model.themeBackgroundSource != themeBackgroundSourceTheme {
		t.Fatalf("picker background = %q, want project theme source", model.themePickerBackground())
	}
	pressKey(model, 'b')
	if model.themeBackgroundSource != themeBackgroundSourceTerminal {
		t.Fatalf("b background = %q, want terminal", model.themePickerBackground())
	}
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
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
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
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
	for _, expected := range []string{"Preview", "Accent background", "Neutral background", "[p] Theme: Project / Selected", "[a] Accent: Project / Theme", "[b] Background: Terminal / Theme", "[m] Mode: Auto / Dark / Light", "SESSION", "[Enter] Apply", "[r] Reload saved", "[Esc] Cancel", "SAVE", "[g] Global", "[c] Project", "Global: /tmp/settings.yaml", "Project: /tmp/kranz.yaml"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("24-row theme picker clipped %q:\n%s", expected, plain)
		}
	}
	plainLines := strings.Split(plain, "\n")
	themePositionLine, controlsLine := -1, -1
	escapeLine, globalLine := -1, -1
	for index, line := range plainLines {
		if strings.Contains(line, "14/19") {
			themePositionLine = index
		}
		if strings.Contains(line, "[p] Theme: Project / Selected") {
			controlsLine = index
		}
		if strings.Contains(line, "[Esc] Cancel") {
			escapeLine = index
		}
		if strings.Contains(line, "Global: /tmp/settings.yaml") {
			globalLine = index
		}
	}
	if escapeLine < 0 || globalLine != escapeLine+3 {
		t.Errorf("config paths are not separated from the controls:\n%s", plain)
	}
	if themePositionLine < 0 || controlsLine < themePositionLine+2 {
		t.Errorf("theme list is not separated from the controls:\n%s", plain)
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
	clickRenderedText(t, model, "Accent: PROJECT · #2AB630")
	if !model.themeColorEditing {
		t.Fatal("clicking the accent field did not open its editor")
	}
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEsc})
	if model.themeColorEditing || model.mode != ModeThemes {
		t.Fatalf("accent field Esc left editing/mode %v/%v", model.themeColorEditing, model.mode)
	}

	clickRenderedText(t, model, "GitHub Light")
	if model.themeUseProject || model.activeTheme.Name != "github-light" {
		t.Fatalf("theme row click = project %v / %s", model.themeUseProject, model.activeTheme.Name)
	}
	clickRenderedText(t, model, "[p] Theme: Project / Selected")
	if !model.themeUseProject || model.activeTheme.Name != "forest" {
		t.Fatalf("project toggle click = project %v / %s", model.themeUseProject, model.activeTheme.Name)
	}
	clickRenderedText(t, model, "[a] Accent: Project / Theme")
	if model.themeAccentSource != themeAccentSourceTheme {
		t.Fatal("accent toggle click did not select the theme default")
	}
	clickRenderedText(t, model, "[b] Background: Terminal / Theme")
	if model.themeBackgroundSource != themeBackgroundSourceTheme {
		t.Fatal("background toggle click did not select a painted theme background")
	}
	clickRenderedText(t, model, "[m] Mode: Auto / Dark / Light")
	if model.themeColorMode != colorModeDark {
		t.Fatal("mode toggle click did not select the dark variant")
	}
	clickRenderedText(t, model, "[Enter] Apply")
	if model.mode != ModeNormal || model.userSettings.Theme != "" || model.userSettings.Accent != "theme" || model.userSettings.Background != "theme" || model.userSettings.ColorMode != "dark" {
		t.Fatalf("theme apply click left mode/settings %v/%#v", model.mode, model.userSettings)
	}

	model.openThemePicker()
	clickRenderedText(t, model, "[r] Reload saved")
	if model.mode != ModeThemes || model.activeTheme.Name != "forest" || model.userSettings != (usersettings.Settings{}) {
		t.Fatalf("reload saved click left mode/theme/settings %v/%q/%#v", model.mode, model.activeTheme.Name, model.userSettings)
	}

	clickRenderedText(t, model, "[g] Global")
	if model.mode != ModeNormal {
		t.Fatalf("global theme save click left mode %v", model.mode)
	}
}

func TestThemePickerRowIsClickableOutsideItsName(t *testing.T) {
	model := NewModel(&config.Config{
		Project: "Clickable", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()

	rendered := model.View()
	for y, line := range strings.Split(ansi.Strip(rendered), "\n") {
		nameStart := strings.Index(line, "GitHub Light")
		if nameStart < 0 {
			continue
		}
		// Click the palette at the end of the row, not the theme name.
		theme, ok := LookupTheme("github-light")
		if !ok {
			t.Fatal("github-light theme not found")
		}
		x := lipgloss.Width(line[:nameStart]) + themeRowNameWidth + lipgloss.Width(themePalettePreview(theme, model.activeTheme.SurfaceAlt)) - 1
		_, _ = model.handleMouseMsg(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		if model.themeUseProject || model.activeTheme.Name != "github-light" {
			t.Fatalf("palette click = project %v / %s", model.themeUseProject, model.activeTheme.Name)
		}
		return
	}
	t.Fatalf("GitHub Light row not found:\n%s", ansi.Strip(rendered))
}

func TestThemePickerUsesThemeIdentityColorsEvenWithProjectAccent(t *testing.T) {
	model := NewModel(&config.Config{
		Project: "Branded", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()
	model.themeUseProject = false
	model.themeAccentSource = themeAccentSourceProject

	theme, ok := LookupTheme("dracula")
	if !ok {
		t.Fatal("dracula theme not found")
	}
	for index, name := range ThemeNames() {
		if name == theme.Name {
			model.themeCursor = index
			break
		}
	}
	model.previewThemePicker()
	wantName := themeNameStyle(theme).Render(theme.DisplayName)
	if setting := model.renderThemePickerThemeSetting(model.cfg.UI.Theme); !strings.Contains(setting, wantName) {
		t.Fatalf("theme setting does not use Dracula identity color: %q", setting)
	}

	preview := themePalettePreview(theme, model.activeTheme.SurfaceAlt)
	plainPreview := ansi.Strip(preview)
	if plainPreview != "● ● ● ●" {
		t.Fatalf("palette preview = %q", plainPreview)
	}
	card := renderThemePreviewCard(theme)
	plainCard := ansi.Strip(card)
	for _, label := range []string{"Preview", "Text", "Muted text", "Accent background", "Neutral background"} {
		if !strings.Contains(plainCard, label) {
			t.Fatalf("theme preview card does not contain %q: %q", label, plainCard)
		}
	}
	if lipgloss.Height(card) != 6 || !strings.Contains(plainCard, "╭") || !strings.Contains(plainCard, "╰") {
		t.Fatalf("theme preview is not a bordered service-like card: %q", plainCard)
	}
	cardLines := strings.Split(plainCard, "\n")
	if strings.Contains(cardLines[1], "─") || strings.Count(cardLines[1], "│") != 2 {
		t.Fatalf("theme preview contains nested border artifacts: %q", plainCard)
	}

	accentSetting := model.renderThemePickerAccentSetting()
	wantAccent := lipgloss.NewStyle().Foreground(lipgloss.Color(model.cfg.UI.Accent)).Bold(true).Render(model.cfg.UI.Accent)
	if !strings.Contains(accentSetting, wantAccent) {
		t.Fatalf("accent setting does not color its hex value: %q", accentSetting)
	}
}

func TestThemePathsWrapWithoutEllipsis(t *testing.T) {
	path := "/a/very/long/project/directory/whose/config/name-must-remain-visible/kranz.yaml"
	lines := renderThemePath("Project", path, 24)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	if strings.Contains(plain, "…") {
		t.Fatalf("wrapped path was truncated: %q", plain)
	}
	compact := strings.Join(strings.Fields(plain), "")
	if compact != "Project:"+path {
		t.Fatalf("wrapped path lost content: %q", plain)
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

func TestThemeAccentEditorSavesCustomColorToProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kranz.yaml")
	data := "project: Custom Accent\nui:\n  theme: forest\n  accent: '#2AB630'\nservices:\n  app:\n    command: exit 0\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(cfg, "test")
	defer model.Shutdown()
	model.openThemePicker()
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("#445566")})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.UI.Accent != "#445566" || model.mode != ModeNormal {
		t.Fatalf("saved custom accent = %q / mode %v", saved.UI.Accent, model.mode)
	}
	// The colour now belongs to the project, so the picker must stop calling it
	// a custom one when it reopens.
	if model.themeAccentSource != themeAccentSourceProject || model.themeCustomAccent != "" {
		t.Fatalf("accent stayed custom after the project save: source=%v custom=%q",
			model.themeAccentSource, model.themeCustomAccent)
	}
	if label := model.themePickerAccentLabel(); label != "PROJECT · #445566" {
		t.Fatalf("accent label after project save = %q", label)
	}
}

// A personal accent that overrides a project accent is one source, not two. The
// picker used to record both at once and let each reader break the tie itself.
func TestThemePickerCustomAccentOverridesProjectAccentAsOneSource(t *testing.T) {
	model := NewModelWithOptions(&config.Config{
		Project: "Both", UI: config.UIConfig{Theme: "forest", Accent: "#00AAFF"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{Settings: usersettings.Settings{Accent: "#FF0000"}})
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()

	if model.themeAccentSource != themeAccentSourceCustom {
		t.Fatalf("personal accent over a project accent = source %v", model.themeAccentSource)
	}
	if got := model.themePickerAccent(); got != "#FF0000" {
		t.Fatalf("resolved accent = %q", got)
	}
	if label, summary := model.themePickerAccentLabel(), model.themePickerSummary(); label != "CUSTOM · #FF0000" || !strings.Contains(summary, label) {
		t.Fatalf("label %q / summary %q", label, summary)
	}

	// The key walks every source that exists and keeps the typed colour as a
	// position in the cycle rather than discarding it.
	press := func() { _, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}) }
	press()
	if model.themeAccentSource != themeAccentSourceProject || model.themePickerAccent() != "#00AAFF" {
		t.Fatalf("first step = source %v / accent %q", model.themeAccentSource, model.themePickerAccent())
	}
	press()
	if model.themeAccentSource != themeAccentSourceTheme || model.themePickerAccent() != "" {
		t.Fatalf("second step = source %v / accent %q", model.themeAccentSource, model.themePickerAccent())
	}
	press()
	if model.themeAccentSource != themeAccentSourceCustom || model.themePickerAccent() != "#FF0000" {
		t.Fatalf("the cycle lost the typed colour: source %v / accent %q",
			model.themeAccentSource, model.themePickerAccent())
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

// The side preview is a fixed-height panel joined next to the theme table, so a
// wider terminal must never buy the preview by pushing the footer or the config
// paths off the bottom of the modal.
func TestThemePickerPreviewNeverCostsControls(t *testing.T) {
	render := func(width, height int) string {
		model := NewModel(&config.Config{
			Project: "Compact", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
			Services: map[string]config.Service{"app": {Command: "exit 0"}},
		}, "test")
		defer model.Shutdown()
		model.width, model.height, model.ready = width, height, true
		model.settingsPath = "/tmp/settings.yaml"
		model.configPaths = []string{"/tmp/kranz.yaml"}
		model.openThemePicker()
		return ansi.Strip(model.renderThemeView())
	}

	controls := []string{"[m] Mode", "[c] Project", "Global: /tmp/settings.yaml", "Project: /tmp/kranz.yaml"}
	for _, height := range []int{18, 20, 22, 24, 30} {
		narrow := render(themePreviewMinWidth-1, height)
		wide := render(themePreviewMinWidth+22, height)
		for _, control := range controls {
			if strings.Contains(narrow, control) && !strings.Contains(wide, control) {
				t.Errorf("at %d rows the preview card hid %q that fits without it:\n%s", height, control, wide)
			}
		}
	}
}

// A committed custom accent must be named by the confirmation notification, not
// just by the picker panel.
func TestThemePickerSummaryNamesCustomAccent(t *testing.T) {
	model := NewModel(&config.Config{
		Project: "Custom", UI: config.UIConfig{Theme: "forest"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()

	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("FF0000")})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})

	summary := model.themePickerSummary()
	if !strings.Contains(summary, "CUSTOM · #FF0000") {
		t.Fatalf("summary does not name the custom accent: %q", summary)
	}
	if label := model.themePickerAccentLabel(); !strings.Contains(summary, label) {
		t.Fatalf("summary %q disagrees with the picker label %q", summary, label)
	}
}

// Palette dots are drawn over the active theme's modal surface, not their own,
// so a dot whose colour equals that surface has to be lifted off it. The
// background dot on a modal painted by the same theme is the worst case.
func TestThemePaletteDotsStayVisibleOnTheModalSurface(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)

	for _, name := range ThemeNames() {
		theme, ok := LookupTheme(name)
		if !ok {
			t.Fatalf("theme %q not found", name)
		}
		for _, surface := range []string{theme.Background, theme.Accent} {
			invisible := lipgloss.NewStyle().Foreground(lipgloss.Color(surface)).Bold(true).Render("●")
			if rendered := themePalettePreview(theme, surface); strings.Contains(rendered, invisible) {
				t.Errorf("%s palette draws a dot in the surface colour %s: %q", name, surface, ansi.Strip(rendered))
			}
		}
	}
}

// A config reload can land while the theme picker is open — pressing r makes it
// near-certain, because reloading the saved appearance leaves the watcher's
// stamps stale. The reload must rebuild the live preview rather than recompute
// the appearance from the config and the saved settings, which would drop every
// uncommitted choice while the panel kept reporting it.
func TestThemePickerSurvivesConfigReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kranz.yaml")
	data := "project: Reload\nui:\n  theme: forest\n  accent: '#2AB630'\nservices:\n  app:\n    command: exit 0\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	model := NewModelWithOptions(cfg, "test", ModelOptions{
		SettingsPath: filepath.Join(dir, "settings.yaml"),
		ConfigPaths:  []string{path},
	})
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("FF0000")})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if model.activeTheme.Accent != "#FF0000" || model.themeBackgroundSource != themeBackgroundSourceTheme || model.themeColorMode != colorModeDark {
		t.Fatalf("picker state before reload = accent %q / background %q / mode %q",
			model.activeTheme.Accent, model.themePickerBackground(), model.themeColorMode)
	}

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = model.Update(configReloadMsg{cfg: reloaded, changed: true})

	if model.activeTheme.Accent != "#FF0000" {
		t.Errorf("reload reverted the typed accent to %q while the panel still shows %q",
			model.activeTheme.Accent, model.themePickerAccentLabel())
	}
	if model.themeBackgroundSource != themeBackgroundSourceTheme || model.themeColorMode != colorModeDark {
		t.Errorf("reload dropped background/mode choices = %q / %q", model.themePickerBackground(), model.themeColorMode)
	}

	_, _ = model.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if model.activeTheme.Accent != "#FF0000" || model.userSettings.Accent != "#FF0000" {
		t.Fatalf("applied appearance = active %q / settings %q", model.activeTheme.Accent, model.userSettings.Accent)
	}
}

// lipgloss paints border cells only through BorderBackground; Background alone
// leaves them transparent and the canvas shows through as a one-cell seam
// around every dialog, because modals sit on SurfaceAlt rather than the canvas.
func TestModalBordersShareTheModalSurface(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()

	model := NewModelWithOptions(&config.Config{
		Project: "Painted", UI: config.UIConfig{Theme: "forest", Background: backgroundTheme},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{Settings: usersettings.Settings{Background: backgroundTheme}})
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	if TerminalCanvas {
		t.Fatal("the theme is expected to paint the canvas for this test")
	}

	surface, ok := parseHex(model.activeTheme.SurfaceAlt)
	if !ok {
		t.Fatalf("modal surface %q is not a hex value", model.activeTheme.SurfaceAlt)
	}
	surfaceSequence := fmt.Sprintf("48;2;%d;%d;%d", int(surface[0]), int(surface[1]), int(surface[2]))

	for name, rendered := range map[string]string{
		"confirmation": renderConfirmationModal("Quit Kranz?", []string{"body"}, "[Enter/y] Yes", "[Esc/n] No"),
		"plain modal":  renderModal("body"),
		"flush modal":  renderFlushModal("body"),
	} {
		for index, line := range strings.Split(rendered, "\n") {
			if !strings.ContainsAny(ansi.Strip(line), "\u256d\u2570\u2502") {
				continue
			}
			if !strings.Contains(line, surfaceSequence) {
				t.Errorf("%s row %d draws its border without the modal surface %s, leaving a canvas seam:\n%q",
					name, index, model.activeTheme.SurfaceAlt, line)
			}
		}
	}
}

// Shift+B mirrors Shift+A: a typed canvas colour becomes a source of its own,
// joins the b cycle instead of replacing it, and re-derives the palette from
// itself the way a theme's own background would.
func TestThemeBackgroundEditorPinsCanvasAndJoinsTheCycle(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	model := NewModel(&config.Config{
		Project: "Canvas", UI: config.UIConfig{Theme: "forest"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()

	if model.themeBackgroundSource != themeBackgroundSourceTerminal {
		t.Fatalf("initial background source = %v", model.themeBackgroundSource)
	}
	if label := model.themeBackgroundControlLabel(); label != "[b] Background: Terminal / Theme" {
		t.Fatalf("label offers Custom before one exists: %q", label)
	}

	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'B'}})
	if !model.themeColorEditing || model.themeColorTarget != themeColorTargetBackground {
		t.Fatalf("Shift+B did not open the background editor: editing=%v target=%v",
			model.themeColorEditing, model.themeColorTarget)
	}
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("204060")})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})

	if model.themeBackgroundSource != themeBackgroundSourceCustom || model.themeCustomBackground != "#204060" {
		t.Fatalf("committed background = source %v / custom %q",
			model.themeBackgroundSource, model.themeCustomBackground)
	}
	if model.activeTheme.Background != "#204060" || model.activeTheme.Surface != "#204060" {
		t.Fatalf("canvas did not take the typed colour: %#v", model.activeTheme)
	}
	if model.activeTheme.SurfaceAlt == "#204060" || model.activeTheme.SurfaceAlt == "" {
		t.Fatalf("elevated surface was not re-derived from the canvas: %q", model.activeTheme.SurfaceAlt)
	}
	if TerminalCanvas {
		t.Fatal("a pinned canvas colour must be painted by Kranz, not the terminal")
	}
	if label := model.themeBackgroundControlLabel(); label != "[b] Background: Terminal / Theme / Custom" {
		t.Fatalf("label does not offer the typed colour: %q", label)
	}

	// b walks all three and comes back to the typed colour.
	for _, want := range []themeBackgroundSource{
		themeBackgroundSourceTerminal, themeBackgroundSourceTheme, themeBackgroundSourceCustom,
	} {
		_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
		if model.themeBackgroundSource != want {
			t.Fatalf("cycle reached %v, want %v", model.themeBackgroundSource, want)
		}
	}
	if model.themeCustomBackground != "#204060" {
		t.Fatalf("cycling discarded the typed colour: %q", model.themeCustomBackground)
	}
}

// A pinned canvas decides the light/dark text set, because Text, Muted, and
// Border are not derived by normalizeTheme and would otherwise keep the values
// the theme shipped with — unreadable on a canvas from the other side.
func TestCustomBackgroundDrivesReadableText(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	for _, background := range []string{"#FFFFFF", "#F5EFE3", "#000000", "#101014"} {
		theme, err := ApplyThemeVariant("forest", "", background, true, false)
		if err != nil {
			t.Fatalf("%s: %v", background, err)
		}
		if theme.Background != background {
			t.Fatalf("%s: canvas = %q", background, theme.Background)
		}
		if got := contrastRatio(theme.Text, theme.Background); got < 4.5 {
			t.Errorf("%s: text contrast %.2f", background, got)
		}
		if got := contrastRatio(theme.Muted, theme.Background); got < 3.0 {
			t.Errorf("%s: muted contrast %.2f", background, got)
		}
	}
	if _, err := ApplyThemeVariant("forest", "", "not-a-colour", true, false); err == nil {
		t.Fatal("a malformed background was accepted")
	}
}

// The canvas colour round-trips through the config the same way the accent
// does: one field carrying either an ownership sentinel or a #RRGGBB value.
func TestCustomBackgroundRoundTripsThroughSettings(t *testing.T) {
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()
	settingsPath := filepath.Join(t.TempDir(), "settings.yaml")
	model := NewModelWithOptions(&config.Config{
		Project: "Canvas", UI: config.UIConfig{Theme: "forest"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{SettingsPath: settingsPath})
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()

	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'B'}})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("204060")})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})

	saved, err := usersettings.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Background != "#204060" {
		t.Fatalf("saved background = %#v", saved)
	}

	reopened := NewModelWithOptions(&config.Config{
		Project: "Canvas", UI: config.UIConfig{Theme: "forest"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{Settings: saved, SettingsPath: settingsPath})
	defer reopened.Shutdown()
	reopened.width, reopened.height, reopened.ready = 110, 32, true
	reopened.openThemePicker()
	if reopened.themeBackgroundSource != themeBackgroundSourceCustom || reopened.themeCustomBackground != "#204060" {
		t.Fatalf("reopened picker lost the canvas: source %v / custom %q",
			reopened.themeBackgroundSource, reopened.themeCustomBackground)
	}
	if reopened.activeTheme.Background != "#204060" {
		t.Fatalf("reopened canvas = %q", reopened.activeTheme.Background)
	}
}

// The Preview card must show what Apply would produce. It used to be built from
// the theme's own defaults with the in-flight editor value patched on top, so it
// ignored the project accent, snapped back the moment Enter committed a custom
// colour, and let a background being typed repaint the accent.
func TestThemePreviewCardTracksThePickerNotTheThemeDefaults(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()

	model := NewModel(&config.Config{
		Project: "Branded", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 120, 34, true
	model.openThemePicker()

	borderOf := func() string {
		t.Helper()
		for _, line := range strings.Split(model.renderThemeView(), "\n") {
			if !strings.Contains(ansi.Strip(line), "─ Preview ─") {
				continue
			}
			segment := line[:strings.Index(line, "Preview")]
			matches := regexp.MustCompile(`38;2;(\d+);(\d+);(\d+)`).FindAllStringSubmatch(segment, -1)
			if len(matches) < 2 {
				t.Fatalf("preview card border colour not found in %q", line)
			}
			group := matches[len(matches)-2]
			return fmt.Sprintf("#%02X%02X%02X", atoi(t, group[1]), atoi(t, group[2]), atoi(t, group[3]))
		}
		t.Fatal("preview card not rendered")
		return ""
	}

	if got := borderOf(); got != "#2AB630" {
		t.Errorf("card ignores the project accent: %s", got)
	}

	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("FF8800")})
	if got := borderOf(); got != "#FF8800" {
		t.Errorf("card does not follow the value being typed: %s", got)
	}
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if got := borderOf(); got != "#FF8800" {
		t.Errorf("card reverted after the accent was committed: %s", got)
	}

	// A background being typed must not repaint the accent.
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'B'}})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("102030")})
	if got := borderOf(); got != "#FF8800" {
		t.Errorf("a background in progress hijacked the accent: %s", got)
	}
}

func atoi(t *testing.T, value string) int {
	t.Helper()
	number, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return number
}

// A colour that was entered — in the project config or typed into the picker —
// is rendered exactly as entered, with no contrast correction of its own.
func TestEnteredColoursAreUsedVerbatim(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()

	// Deliberately awkward colours: every one of these used to be shifted.
	for _, accent := range []string{"#0969DA", "#B91C1C", "#155E29", "#2AB630", "#FF8800"} {
		theme, err := BuildTheme("forest", accent, "", true)
		if err != nil {
			t.Fatalf("%s: %v", accent, err)
		}
		if theme.Accent != accent || theme.AccentText != accent {
			t.Errorf("accent %s came back as %s / %s", accent, theme.Accent, theme.AccentText)
		}
	}

	for _, background := range []string{"#204060", "#0A0A0A", "#FAFAFA"} {
		theme, err := BuildTheme("forest", "#B91C1C", background, true)
		if err != nil {
			t.Fatalf("%s: %v", background, err)
		}
		if theme.Background != background || theme.Surface != background {
			t.Errorf("background %s came back as %s / %s", background, theme.Background, theme.Surface)
		}
		if theme.Accent != "#B91C1C" {
			t.Errorf("background %s disturbed the accent: %s", background, theme.Accent)
		}
	}

	// The installed styles carry the entered colour, not a corrected variant.
	model := NewModelWithOptions(&config.Config{
		Project: "Deep", UI: config.UIConfig{Theme: "forest", Accent: "#B91C1C", Background: backgroundTheme},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{Settings: usersettings.Settings{Background: backgroundTheme}})
	defer model.Shutdown()
	if got := fmt.Sprint(ModalStyle.GetBorderTopForeground()); got != "#B91C1C" {
		t.Errorf("modal border = %s, want the entered accent #B91C1C", got)
	}
	if got := fmt.Sprint(FocusedPanelStyle.GetBorderTopForeground()); got != "#B91C1C" {
		t.Errorf("focused panel border = %s, want the entered accent #B91C1C", got)
	}
}

// Typing a colour adds a Custom position to the a and b labels. The key-hint
// column reserves room for it up front, because sizing to the current labels
// made the modal jump nine columns wider the moment a custom colour appeared.
func TestThemePickerWidthDoesNotChangeWhenCustomColoursAppear(t *testing.T) {
	// The modal's own top border, measured as its unbroken run of box drawing.
	modalWidth := func(model *Model) int {
		t.Helper()
		for _, line := range strings.Split(ansi.Strip(model.renderThemeView()), "\n") {
			index := strings.Index(line, "╭")
			if index < 5 {
				continue // a dashboard panel at the screen edge, not the modal
			}
			run := 0
			for _, symbol := range line[index:] {
				if symbol == '─' {
					run++
				} else if run > 0 {
					break
				}
			}
			if run > 10 {
				return run + 2
			}
		}
		t.Fatal("theme picker modal not found")
		return 0
	}

	for _, projectAccent := range []string{"#2AB630", ""} {
		model := NewModel(&config.Config{
			Project: "Widths", UI: config.UIConfig{Theme: "forest", Accent: projectAccent},
			Services: map[string]config.Service{"app": {Command: "exit 0"}},
		}, "test")
		model.width, model.height, model.ready = 120, 34, true
		model.openThemePicker()
		want := modalWidth(model)

		_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'B'}})
		_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("204060")})
		_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
		if got := modalWidth(model); got != want {
			t.Errorf("project accent %q: a custom background changed the width %d → %d", projectAccent, want, got)
		}

		_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
		_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("FF8800")})
		_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyEnter})
		if got := modalWidth(model); got != want {
			t.Errorf("project accent %q: a custom accent changed the width %d → %d", projectAccent, want, got)
		}
		model.Shutdown()
	}
}

// A flush modal drops the style's horizontal padding so a full-width row can
// reach the border, and every content line then carries its own two-space
// indent. Nothing balanced that on the right, so the widest row — the key hints
// — sat flush against the frame.
func TestThemePickerKeepsEqualSideMargins(t *testing.T) {
	model := NewModel(&config.Config{
		Project: "Margins", UI: config.UIConfig{Theme: "forest", Accent: "#2AB630"},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test")
	defer model.Shutdown()
	model.width, model.height, model.ready = 120, 34, true
	model.openThemePicker()

	lines := strings.Split(ansi.Strip(model.renderThemeView()), "\n")
	left, right := -1, -1
	for _, line := range lines {
		runes := []rune(line)
		for index, symbol := range runes {
			if symbol != '╭' || index <= 5 {
				continue // a dashboard panel at the screen edge, not the modal
			}
			left = index
			for scan := index; scan < len(runes); scan++ {
				if runes[scan] == '╮' {
					right = scan
					break
				}
			}
			break
		}
		if left >= 0 {
			break
		}
	}
	if left < 0 || right <= left {
		t.Fatal("theme picker modal not found")
	}

	// Cells, not bytes: the rows are full of multi-byte box drawing. Only rows
	// framed by │ are content; the ╭─╮ and ╰─╯ rows are the frame itself.
	widest := 0
	for _, line := range lines {
		runes := []rune(line)
		if right >= len(runes) || runes[left] != '│' || runes[right] != '│' {
			continue
		}
		inner := string(runes[left+1 : right])
		if strings.TrimSpace(inner) == "" {
			continue
		}
		widest = max(widest, lipgloss.Width(strings.TrimRight(inner, " ")))
	}
	if widest == 0 {
		t.Fatal("no content rows found inside the modal")
	}
	if gutter := (right - left - 1) - widest; gutter != modalSideMargin {
		t.Errorf("the widest row leaves %d cells before the frame, want %d to match the left indent",
			gutter, modalSideMargin)
	}
}

// The colour being typed is shown as the field's own background, so it reads as
// an area rather than as a coloured glyph — a pale canvas colour on a pale
// surface used to leave nothing visible. Its digits are lifted off it so the
// field never swallows its own text.
func TestColorEditorFieldCarriesTheColour(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)
	defer func() { _, _ = ApplyTheme(DefaultTheme, "") }()

	light := false
	model := NewModelWithOptions(&config.Config{
		Project: "Swatch", UI: config.UIConfig{Theme: "forest", Background: backgroundTheme},
		Services: map[string]config.Service{"app": {Command: "exit 0"}},
	}, "test", ModelOptions{DarkBackground: &light, Settings: usersettings.Settings{Background: backgroundTheme}})
	defer model.Shutdown()
	model.width, model.height, model.ready = 110, 32, true
	model.openThemePicker()

	// A pale canvas colour on a pale surface: the case from the report.
	const canvas = "#F2F7F3"
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'B'}})
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F2F7F3")})
	row := model.renderThemePickerBackgroundSetting()

	foreground := readableTextOn(canvas, model.activeTheme.Text)
	field := lipgloss.NewStyle().
		Foreground(lipgloss.Color(foreground)).
		Background(lipgloss.Color(canvas)).
		Bold(true)
	if !strings.Contains(row, field.Render("#")) {
		t.Errorf("the field does not carry the colour as its background: %q", row)
	}
	// Every colour the field can hold, including the mid-luminance ones where a
	// single-direction correction stalls short of the goal.
	for _, colour := range []string{canvas, "#1A1F1C", "#FF8800", "#808080", "#0969DA", "#FFFF00"} {
		if got := contrastRatio(readableTextOn(colour, model.activeTheme.Text), colour); got < 4.5 {
			t.Errorf("digits over %s read at only %.2f", colour, got)
		}
	}
	if !strings.Contains(ansi.Strip(row), "●") {
		t.Errorf("the swatch dot is missing: %q", ansi.Strip(row))
	}

	// An incomplete value keeps the row the same width, so committing one cannot
	// shift what follows.
	valid := lipgloss.Width(ansi.Strip(row))
	_, _ = model.handleThemeKeys(tea.KeyMsg{Type: tea.KeyBackspace})
	partial := model.renderThemePickerBackgroundSetting()
	if got := lipgloss.Width(ansi.Strip(partial)); got != valid {
		t.Errorf("row width changed with an incomplete value: %d vs %d", got, valid)
	}
	if !strings.Contains(ansi.Strip(partial), "○") {
		t.Errorf("an incomplete value does not reserve the swatch: %q", ansi.Strip(partial))
	}
}
