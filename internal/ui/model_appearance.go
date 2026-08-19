package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kranz-org/kranz/internal/config"
	usersettings "github.com/kranz-org/kranz/internal/settings"
)

// Appearance resolution and the live theme picker. Theme, accent, background
// ownership, and colour mode are four independent choices, and this file owns
// how they are resolved, previewed, and persisted.

// themeAccentSource names where the picker's accent comes from. It replaces a
// pair of independent fields that could both describe the accent at once — a
// personal custom colour alongside a project accent — leaving every reader to
// resolve the contradiction with its own precedence chain.
type themeAccentSource uint8

const (
	// themeAccentSourceTheme keeps whatever accent the selected theme defines.
	themeAccentSourceTheme themeAccentSource = iota
	// themeAccentSourceProject pins the accent declared by the project config.
	themeAccentSourceProject
	// themeAccentSourceCustom pins themeCustomAccent, typed in the editor or
	// carried over from the user settings.
	themeAccentSourceCustom
)

// themeBackgroundSource names who owns the canvas. Terminal and Theme are the
// two long-standing owners; Custom pins a colour the user typed.
type themeBackgroundSource uint8

const (
	// themeBackgroundSourceTerminal leaves the canvas unpainted so the terminal
	// profile supplies its exact background.
	themeBackgroundSourceTerminal themeBackgroundSource = iota
	// themeBackgroundSourceTheme paints the canvas with the theme's own surface.
	themeBackgroundSourceTheme
	// themeBackgroundSourceCustom paints themeCustomBackground.
	themeBackgroundSourceCustom
)

// themeColorTarget selects which colour the shared hex editor is editing.
type themeColorTarget uint8

const (
	themeColorTargetAccent themeColorTarget = iota
	themeColorTargetBackground
)

func effectiveAppearance(project config.UIConfig, user usersettings.Settings) (theme, accent, background, colorMode string) {
	theme = project.Theme
	accent = project.Accent
	background = normalizeBackgroundSource(project.Background)
	colorMode = normalizeColorMode(project.ColorMode)
	if theme == "" {
		theme = DefaultTheme
	}
	if user.Theme != "" && user.Theme != "auto" {
		theme = user.Theme
		// A user-selected theme uses its own palette. The project's accent only
		// belongs to the project's theme and must not make every preview blue.
		accent = ""
	}
	if user.Accent == "theme" {
		accent = ""
	} else if user.Accent != "" && user.Accent != "auto" {
		accent = user.Accent
	}
	if user.Background != "" {
		background = normalizeBackgroundSource(user.Background)
	}
	if user.ColorMode != "" {
		colorMode = normalizeColorMode(user.ColorMode)
	}
	return theme, accent, background, colorMode
}

// normalizeBackgroundSource canonicalises a stored background. Like Accent, the
// field multiplexes sentinels and a colour: "terminal" and "theme" name who owns
// the canvas, and a #RRGGBB value pins one of the user's own.
func normalizeBackgroundSource(source string) string {
	trimmed := strings.TrimSpace(source)
	if hexColorPattern.MatchString(trimmed) {
		return strings.ToUpper(trimmed)
	}
	switch strings.ToLower(trimmed) {
	case backgroundTheme:
		return backgroundTheme
	default:
		return backgroundTerminal
	}
}

// customBackgroundColor reports the pinned canvas colour, if the stored value is
// one rather than an ownership sentinel.
func customBackgroundColor(source string) (string, bool) {
	normalized := normalizeBackgroundSource(source)
	if normalized == backgroundTerminal || normalized == backgroundTheme {
		return "", false
	}
	return normalized, true
}

func normalizeColorMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case colorModeDark:
		return colorModeDark
	case colorModeLight:
		return colorModeLight
	default:
		return colorModeAuto
	}
}

func colorModeIsDark(mode string, terminalDark bool) bool {
	switch normalizeColorMode(mode) {
	case colorModeDark:
		return true
	case colorModeLight:
		return false
	default:
		return terminalDark
	}
}

func applyAppearance(name, accent, background, colorMode string, terminalDark bool) (Theme, error) {
	custom, isCustom := customBackgroundColor(background)
	return ApplyThemeVariant(
		name,
		accent,
		custom,
		colorModeIsDark(colorMode, terminalDark),
		// A pinned canvas colour is by definition painted by Kranz, so it can
		// never leave the canvas to the terminal.
		!isCustom && normalizeBackgroundSource(background) == backgroundTerminal,
	)
}

func newThemeColorInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	// The six-digit limit is enforced by sanitizeThemeColorInput instead of
	// CharLimit. A limit here would truncate before sanitizing, so pasting the
	// seven-character "#FF0000" would keep "#FF000" and lose the last digit;
	// sanitizing first drops the "#" and leaves all six digits.
	input.CharLimit = 0
	input.Width = 6
	return input
}

func (m *Model) pollSystemAppearance() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		dark, available := detectSystemDarkMode()
		return systemAppearanceMsg{dark: dark, available: available}
	})
}

func (m *Model) probeTerminalBackground(force bool) tea.Cmd {
	if !terminalBackgroundProbeSupported() || m.backgroundProbeBusy || (!force && time.Since(m.lastBackgroundProbe) < time.Second) {
		return nil
	}
	m.backgroundProbeBusy = true
	probe := &terminalBackgroundProbe{}
	return tea.Exec(probe, func(err error) tea.Msg {
		return backgroundColorMsg{dark: probe.dark, err: err}
	})
}

func (m *Model) applyDetectedBackground(dark bool, source string) tea.Cmd {
	if dark == m.terminalDark {
		return nil
	}
	m.terminalDark = dark
	_, _, _, colorMode := effectiveAppearance(m.cfg.UI, m.userSettings)
	if m.mode == ModeThemes {
		colorMode = m.themeColorMode
	}
	if colorMode != colorModeAuto {
		return nil
	}
	if m.mode == ModeThemes {
		m.previewThemePicker()
	} else if err := m.applyEffectiveAppearance(); err != nil {
		m.addNotification("appearance", "Could not adapt to terminal background: "+err.Error(), config.LogError)
		return nil
	}
	mode := "light"
	if dark {
		mode = "dark"
	}
	m.addNotification("appearance", source+" appearance changed to "+mode, config.LogInfo)
	return tea.ClearScreen
}

func (m *Model) openThemePicker() {
	m.themeBefore = m.activeTheme
	m.settingsBefore = m.userSettings
	m.syncThemePickerControls()
	m.mode = ModeThemes
}

func (m *Model) syncThemePickerControls() {
	m.themeColorEditing = false
	m.themeColorReplace = false
	m.themeColorError = ""
	m.themeColorInput.Blur()
	m.themeCursor = 0
	for index, name := range ThemeNames() {
		if name == m.activeTheme.Name {
			m.themeCursor = index
			break
		}
	}
	m.themeUseProject = m.userSettings.Theme == "" || m.userSettings.Theme == "auto"
	projectAccent := strings.TrimSpace(m.cfg.UI.Accent)
	m.themeAccentChanged = false
	m.themeCustomAccent = ""
	// A personal accent that differs from the project's is a custom one, and it
	// takes the source outright: the project accent it overrides is not a second
	// simultaneous answer.
	switch {
	case isCustomAccent(m.userSettings.Accent, m.cfg.UI.Accent):
		m.themeCustomAccent = strings.ToUpper(strings.TrimSpace(m.userSettings.Accent))
		m.themeAccentSource = themeAccentSourceCustom
	case projectAccent != "" && m.userSettings.Accent != "theme" &&
		(m.themeUseProject || strings.EqualFold(m.userSettings.Accent, projectAccent)):
		m.themeAccentSource = themeAccentSourceProject
	default:
		m.themeAccentSource = themeAccentSourceTheme
	}
	var background string
	_, _, background, m.themeColorMode = effectiveAppearance(m.cfg.UI, m.userSettings)
	m.themeCustomBackground = ""
	switch custom, isCustom := customBackgroundColor(background); {
	case isCustom:
		m.themeCustomBackground = custom
		m.themeBackgroundSource = themeBackgroundSourceCustom
	case background == backgroundTheme:
		m.themeBackgroundSource = themeBackgroundSourceTheme
	default:
		m.themeBackgroundSource = themeBackgroundSourceTerminal
	}
}

// themePickerAccentSources lists the accent sources the a key can reach. Custom
// only joins once a colour exists, and Project only when the project declares
// one, so the cycle never stops on a position that has nothing behind it.
func (m *Model) themePickerAccentSources() []themeAccentSource {
	sources := make([]themeAccentSource, 0, 3)
	if strings.TrimSpace(m.cfg.UI.Accent) != "" {
		sources = append(sources, themeAccentSourceProject)
	}
	sources = append(sources, themeAccentSourceTheme)
	if m.themeCustomAccent != "" {
		sources = append(sources, themeAccentSourceCustom)
	}
	return sources
}

// themePickerBackgroundSources lists the canvas owners the b key can reach.
// Terminal and Theme always exist, so unlike the accent this cycle is never
// degenerate.
func (m *Model) themePickerBackgroundSources() []themeBackgroundSource {
	sources := []themeBackgroundSource{themeBackgroundSourceTerminal, themeBackgroundSourceTheme}
	if m.themeCustomBackground != "" {
		sources = append(sources, themeBackgroundSourceCustom)
	}
	return sources
}

func (m *Model) handleThemeKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.themeColorEditing {
		return m.handleThemeColorKeys(msg)
	}
	names := ThemeNames()
	switch msg.String() {
	case "up", "k":
		m.themeCursor = (m.themeCursor - 1 + len(names)) % len(names)
		m.themeUseProject = false
		m.previewThemePicker()
	case "down", "j":
		m.themeCursor = (m.themeCursor + 1) % len(names)
		m.themeUseProject = false
		m.previewThemePicker()
	case "enter":
		m.applyThemePicker(names)
	case "r", "R":
		m.reloadSavedAppearance()
	case "g", "G":
		m.beginThemeSaveConfirmation(themeSaveGlobal)
	case "c", "C":
		m.beginThemeSaveConfirmation(themeSaveProject)
	case "p", "P":
		m.themeUseProject = !m.themeUseProject
		m.previewThemePicker()
	case "A":
		return m, m.beginThemeColorEdit(themeColorTargetAccent)
	case "a":
		return m, m.cycleThemeAccentSource()
	case "B":
		return m, m.beginThemeColorEdit(themeColorTargetBackground)
	case "b":
		m.cycleThemeBackgroundSource()
	case "m", "M":
		m.cycleThemeColorMode()
	case "esc", "q":
		m.cancelThemePicker()
	}
	return m, nil
}

func (m *Model) beginThemeSaveConfirmation(scope themeSaveScope) {
	if scope == themeSaveProject && m.themeProjectConfigPath() == "" {
		m.addNotification("settings", "No project configuration path is available", config.LogError)
		return
	}
	m.themeSaveScope = scope
	m.mode = ModeConfirmThemeSave
}

func (m *Model) handleConfirmThemeSaveKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		scope := m.themeSaveScope
		m.themeSaveScope = themeSaveNone
		switch scope {
		case themeSaveProject:
			m.saveThemePickerToProject()
		case themeSaveGlobal:
			m.saveThemePicker(ThemeNames())
		}
	case "n", "N", "esc":
		m.themeSaveScope = themeSaveNone
		m.mode = ModeThemes
	}
	return m, nil
}

// beginThemeColorEdit opens the shared hex editor on one of the two colours,
// seeded with what the preview currently shows so editing starts from the
// visible value rather than from an empty field.
func (m *Model) beginThemeColorEdit(target themeColorTarget) tea.Cmd {
	m.themeColorTarget = target
	seed := m.activeTheme.Accent
	if target == themeColorTargetBackground {
		seed = m.activeTheme.Background
	}
	value := strings.TrimPrefix(strings.TrimSpace(seed), "#")
	m.themeColorInput.SetValue(strings.ToUpper(value))
	m.themeColorInput.CursorEnd()
	m.themeColorError = ""
	m.themeColorEditing = true
	m.themeColorReplace = true
	return m.themeColorInput.Focus()
}

func (m *Model) handleThemeColorKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.themeColorInput.Blur()
		m.themeColorEditing = false
		m.themeColorReplace = false
		m.themeColorError = ""
		return m, nil
	case "enter":
		color := "#" + strings.ToUpper(m.themeColorInput.Value())
		if !hexColorPattern.MatchString(color) {
			m.themeColorError = "Enter 6 hex digits"
			return m, nil
		}
		if m.themeColorTarget == themeColorTargetBackground {
			m.themeCustomBackground = color
			m.themeBackgroundSource = themeBackgroundSourceCustom
		} else {
			m.themeCustomAccent = color
			m.themeAccentChanged = true
			m.themeAccentSource = themeAccentSourceCustom
		}
		m.themeColorInput.Blur()
		m.themeColorEditing = false
		m.themeColorReplace = false
		m.themeColorError = ""
		m.previewThemePicker()
		return m, nil
	case "tab", "shift+tab":
		return m, nil
	}

	if msg.Type == tea.KeyRunes {
		value := sanitizeThemeColorValue(string(msg.Runes))
		if value == "" {
			return m, nil
		}
		if m.themeColorReplace {
			m.themeColorInput.SetValue("")
			m.themeColorInput.CursorStart()
		}
		m.themeColorReplace = false
		msg.Runes = []rune(value)
	} else {
		if msg.String() == "ctrl+v" && m.themeColorReplace {
			m.themeColorInput.SetValue("")
			m.themeColorInput.CursorStart()
		}
		m.themeColorReplace = false
	}
	m.themeColorError = ""
	var command tea.Cmd
	m.themeColorInput, command = m.themeColorInput.Update(msg)
	m.sanitizeThemeColorInput()
	return m, command
}

// sanitizeThemeColorInput keeps the field at six hexadecimal digits. Byte
// slicing and byte lengths are safe against the rune cursor position only
// because sanitizeThemeColorValue admits ASCII hex digits and nothing else.
func (m *Model) sanitizeThemeColorInput() {
	value := m.themeColorInput.Value()
	position := m.themeColorInput.Position()
	sanitized := sanitizeThemeColorValue(value)
	if len(sanitized) > 6 {
		sanitized = sanitized[:6]
	}
	if sanitized == value {
		return
	}
	m.themeColorInput.SetValue(sanitized)
	m.themeColorInput.SetCursor(min(position, len(sanitized)))
}

func sanitizeThemeColorValue(value string) string {
	var result strings.Builder
	for _, character := range value {
		if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F') {
			result.WriteRune(character)
		}
	}
	return strings.ToUpper(result.String())
}

func (m *Model) reloadSavedAppearance() {
	projectAppearance := m.cfg.UI
	if len(m.configPaths) > 0 {
		loaded, err := config.LoadFiles(m.configPaths)
		if err != nil {
			m.addNotification("appearance", "Could not reload project appearance: "+err.Error(), config.LogError)
			return
		}
		projectAppearance = loaded.UI
	}

	userSettings, err := usersettings.Load(m.settingsPath)
	if err != nil {
		m.addNotification("appearance", "Could not reload global appearance: "+err.Error(), config.LogError)
		return
	}
	name, accent, background, colorMode := effectiveAppearance(projectAppearance, userSettings)
	theme, err := applyAppearance(name, accent, background, colorMode, m.terminalDark)
	if err != nil {
		m.addNotification("appearance", "Could not apply saved appearance: "+err.Error(), config.LogError)
		return
	}

	m.cfg.UI = projectAppearance
	m.userSettings = userSettings
	m.activeTheme = theme
	m.themeBefore = theme
	m.settingsBefore = userSettings
	m.syncThemePickerControls()
	m.addNotification("appearance", "Saved appearance reloaded from configuration", config.LogInfo)
}

func (m *Model) applyThemePicker(names []string) {
	m.updateThemePickerSettings(names)
	m.addNotification("appearance", "Appearance applied for this session: "+m.themePickerSummary(), config.LogInfo)
	m.mode = ModeNormal
}

func (m *Model) saveThemePicker(names []string) {
	m.updateThemePickerSettings(names)
	if err := m.persistSettings(); err != nil {
		m.addNotification("settings", err.Error(), config.LogError)
	} else {
		m.addNotification("appearance", "Appearance saved globally: "+m.themePickerSummary(), config.LogInfo)
	}
	m.mode = ModeNormal
}

func (m *Model) updateThemePickerSettings(names []string) {
	if m.themeUseProject {
		m.userSettings.Theme = ""
	} else {
		m.userSettings.Theme = names[m.themeCursor]
	}
	if m.themeAccentChanged {
		switch {
		case m.themeAccentSource == themeAccentSourceCustom:
			m.userSettings.Accent = m.themeCustomAccent
		case m.themeAccentSource == themeAccentSourceTheme:
			m.userSettings.Accent = "theme"
		case m.themeUseProject:
			// The project owns both the theme and its accent, so no personal
			// override is needed to reproduce this choice.
			m.userSettings.Accent = ""
		default:
			m.userSettings.Accent = strings.TrimSpace(m.cfg.UI.Accent)
		}
	}
	projectBackground := normalizeBackgroundSource(m.cfg.UI.Background)
	if m.themePickerBackground() == projectBackground {
		m.userSettings.Background = ""
	} else {
		m.userSettings.Background = m.themePickerBackground()
	}
	projectColorMode := normalizeColorMode(m.cfg.UI.ColorMode)
	if m.themeColorMode == projectColorMode {
		m.userSettings.ColorMode = ""
	} else {
		m.userSettings.ColorMode = m.themeColorMode
	}
}

func (m *Model) saveThemePickerToProject() {
	path := m.themeProjectConfigPath()
	if path == "" {
		m.addNotification("settings", "No project configuration path is available", config.LogError)
		return
	}
	appearance := config.UIConfig{
		Theme:      m.activeTheme.Name,
		Background: m.themePickerBackground(),
		ColorMode:  m.themeColorMode,
	}
	// An empty resolved accent means the theme keeps its own, which the project
	// file records by leaving the field out rather than pinning a colour.
	appearance.Accent = strings.ToUpper(m.themePickerAccent())
	if err := config.SaveUIAppearance(path, appearance); err != nil {
		m.addNotification("settings", err.Error(), config.LogError)
		return
	}
	// The project file is already authoritative at this point, even if clearing
	// the personal overrides below fails. Keep the in-memory config aligned with
	// disk and then reapply any overrides that could not be removed.
	m.cfg.UI = appearance

	previousSettings := m.userSettings
	m.userSettings.Theme = ""
	m.userSettings.Accent = ""
	m.userSettings.Background = ""
	m.userSettings.ColorMode = ""
	if err := m.persistSettings(); err != nil {
		m.userSettings = previousSettings
		_ = m.applyEffectiveAppearance()
		m.addNotification("settings", "Project appearance was saved, but user overrides could not be cleared: "+err.Error(), config.LogWarn)
	} else {
		m.themeUseProject = true
		// Whatever accent was just written now belongs to the project, so a
		// colour that arrived here as a custom one stops being custom: keeping
		// themeCustomAccent would leave the picker calling the project's own
		// accent "CUSTOM".
		m.themeCustomAccent = ""
		m.themeAccentSource = themeAccentSourceTheme
		if appearance.Accent != "" {
			m.themeAccentSource = themeAccentSourceProject
		}
		m.addNotification("appearance", "Project appearance saved to "+path, config.LogInfo)
	}
	m.app.AcknowledgeExternalWrite()
	m.mode = ModeNormal
}

// cycleThemeAccentSource walks the accent sources that actually exist. A typed
// colour joins the cycle instead of being discarded, which is the only scoped
// way back to it once the user moves off. When the project declares no accent
// and nothing has been typed the cycle would be a single position, so the key
// opens the editor instead — the behaviour the README shortcut table documents.
func (m *Model) cycleThemeAccentSource() tea.Cmd {
	sources := m.themePickerAccentSources()
	if len(sources) == 1 {
		return m.beginThemeColorEdit(themeColorTargetAccent)
	}
	m.themeAccentChanged = true
	m.themeAccentSource = sources[(indexOfAccentSource(sources, m.themeAccentSource)+1)%len(sources)]
	m.previewThemePicker()
	return nil
}

func (m *Model) cycleThemeBackgroundSource() {
	sources := m.themePickerBackgroundSources()
	m.themeBackgroundSource = sources[(indexOfBackgroundSource(sources, m.themeBackgroundSource)+1)%len(sources)]
	m.previewThemePicker()
}

func indexOfBackgroundSource(sources []themeBackgroundSource, source themeBackgroundSource) int {
	for index, candidate := range sources {
		if candidate == source {
			return index
		}
	}
	return 0
}

func indexOfAccentSource(sources []themeAccentSource, source themeAccentSource) int {
	for index, candidate := range sources {
		if candidate == source {
			return index
		}
	}
	return 0
}

func (m *Model) cycleThemeColorMode() {
	switch m.themeColorMode {
	case colorModeAuto:
		m.themeColorMode = colorModeDark
	case colorModeDark:
		m.themeColorMode = colorModeLight
	default:
		m.themeColorMode = colorModeAuto
	}
	m.previewThemePicker()
}

func (m *Model) cancelThemePicker() {
	m.themeColorInput.Blur()
	m.themeColorEditing = false
	m.userSettings = m.settingsBefore
	m.activeTheme = m.themeBefore
	applyPalette(m.themeBefore)
	m.mode = ModeNormal
}

func (m *Model) previewThemePicker() {
	name := ThemeNames()[m.themeCursor]
	if m.themeUseProject {
		name = m.cfg.UI.Theme
		if name == "" {
			name = DefaultTheme
		}
	}
	theme, err := applyAppearance(name, m.themePickerAccent(), m.themePickerBackground(), m.themeColorMode, m.terminalDark)
	if err == nil {
		m.activeTheme = theme
	}
}

func (m *Model) applyEffectiveAppearance() error {
	name, accent, background, colorMode := effectiveAppearance(m.cfg.UI, m.userSettings)
	theme, err := applyAppearance(name, accent, background, colorMode, m.terminalDark)
	if err != nil {
		return err
	}
	m.activeTheme = theme
	return nil
}

func (m *Model) themeProjectConfigPath() string {
	if len(m.configPaths) == 0 {
		return ""
	}
	return m.configPaths[len(m.configPaths)-1]
}

// themePickerAccent resolves the accent the picker currently represents. An
// empty result means the selected theme keeps its own accent. Preview, label,
// and both save paths read the answer from here so the source cannot be
// interpreted differently in each of them.
func (m *Model) themePickerAccent() string {
	switch m.themeAccentSource {
	case themeAccentSourceCustom:
		return m.themeCustomAccent
	case themeAccentSourceProject:
		return strings.TrimSpace(m.cfg.UI.Accent)
	default:
		return ""
	}
}

// themePickerBackground resolves the canvas the picker currently represents, in
// the same shape the config and the user settings store: the "terminal" or
// "theme" sentinel, or a pinned #RRGGBB colour.
func (m *Model) themePickerBackground() string {
	switch m.themeBackgroundSource {
	case themeBackgroundSourceCustom:
		return m.themeCustomBackground
	case themeBackgroundSourceTheme:
		return backgroundTheme
	default:
		return backgroundTerminal
	}
}

func isCustomAccent(accent, projectAccent string) bool {
	accent = strings.TrimSpace(accent)
	return accent != "" && accent != "auto" && accent != "theme" && !strings.EqualFold(accent, strings.TrimSpace(projectAccent))
}

// themePickerSummary describes the applied appearance for the confirmation
// notification. It reuses the picker's own label helpers on purpose: a second
// set of conditions is how the two drifted apart before, when the panel read
// themeCustomAccent and this summary still only knew Theme versus Project.
func (m *Model) themePickerSummary() string {
	return strings.Join(m.themePickerSummaryLines(), " / ")
}

func (m *Model) themePickerSummaryLines() []string {
	projectTheme := m.cfg.UI.Theme
	if projectTheme == "" {
		projectTheme = DefaultTheme
	}
	return []string{
		"Theme " + m.themePickerThemeLabel(projectTheme),
		"Accent " + m.themePickerAccentLabel(),
		"Background " + m.themePickerBackgroundLabel(),
		"Mode " + m.themePickerColorModeLabel(),
	}
}

func (m *Model) persistSettings() error {
	if m.settingsPath == "" {
		return nil
	}
	return usersettings.Save(m.settingsPath, m.userSettings)
}
