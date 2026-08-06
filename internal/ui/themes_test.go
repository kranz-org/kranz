package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// restoreDefaultTheme reverts the package-level theme state once the test ends.
// Applying a built-in theme cannot fail, so a failure means the theme registry
// itself is broken and the test should say so rather than continue silently.
func restoreDefaultTheme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := ApplyTheme(DefaultTheme, ""); err != nil {
			t.Errorf("restore default theme: %v", err)
		}
	})
}

func TestEveryThemeCanBeApplied(t *testing.T) {
	restoreDefaultTheme(t)
	if len(ThemeNames()) < 16 {
		t.Fatalf("theme count = %d, want at least 16", len(ThemeNames()))
	}
	for _, name := range ThemeNames() {
		theme, err := ApplyTheme(name, "")
		if err != nil {
			t.Errorf("ApplyTheme(%q) error = %v", name, err)
		}
		if theme.Accent == "" || theme.AccentText == "" || theme.Info == "" || theme.Data == "" ||
			theme.Selection == "" || theme.SelectText == "" || theme.Text == "" ||
			theme.Background == "" || theme.Surface == "" || theme.SurfaceAlt == "" {
			t.Errorf("theme %q is incomplete: %#v", name, theme)
		}
		if ratio := contrastRatio(theme.Text, theme.Surface); ratio < 4.5 {
			t.Errorf("theme %q text contrast = %.2f, want at least 4.5", name, ratio)
		}
		if ratio := contrastRatio(theme.AccentText, theme.Surface); ratio < 4.5 {
			t.Errorf("theme %q accent text contrast = %.2f, want at least 4.5", name, ratio)
		}
		if ratio := contrastRatio(theme.Info, theme.Surface); ratio < 4.5 {
			t.Errorf("theme %q info contrast = %.2f, want at least 4.5", name, ratio)
		}
		if ratio := contrastRatio(theme.Data, theme.Surface); ratio < 4.5 {
			t.Errorf("theme %q data contrast = %.2f, want at least 4.5", name, ratio)
		}
		if ratio := contrastRatio(theme.SelectText, theme.Selection); ratio < 4.5 {
			t.Errorf("theme %q selection contrast = %.2f, want at least 4.5", name, ratio)
		}
		if ratio := contrastRatio(theme.Text, theme.SurfaceAlt); ratio < 4.5 {
			t.Errorf("theme %q elevated surface contrast = %.2f, want at least 4.5", name, ratio)
		}
		if theme.Background != theme.Surface {
			t.Errorf("theme %q splits the dashboard canvas: %#v", name, theme)
		}
		if name != "high-contrast" && theme.Background == theme.SurfaceAlt {
			t.Errorf("theme %q has no intentional elevated surface", name)
		}
	}
}

func TestHighContrastThemeUsesMaximumCanvasContrast(t *testing.T) {
	restoreDefaultTheme(t)
	theme, err := ApplyTheme("high-contrast", "")
	if err != nil {
		t.Fatal(err)
	}
	if ratio := contrastRatio(theme.Text, theme.Background); ratio != 21 {
		t.Fatalf("high contrast ratio = %.2f, want 21", ratio)
	}
	if theme.Border != "#FFFFFF" || theme.Surface != "#000000" {
		t.Fatalf("high contrast structure = %#v", theme)
	}
}

func TestLightThemesProvideExplicitReadableSurfaces(t *testing.T) {
	for _, name := range []string{"github-light", "solarized-light", "cream"} {
		theme, ok := LookupTheme(name)
		if !ok {
			t.Fatalf("missing light theme %q", name)
		}
		if relativeLuminance(mustParseColor(t, theme.Background)) < 0.7 {
			t.Errorf("theme %q background is not light: %s", name, theme.Background)
		}
		if contrastRatio(theme.Text, theme.Surface) < 4.5 {
			t.Errorf("theme %q surface text is unreadable", name)
		}
	}
}

func TestThemeAdaptsToDetectedTerminalBackground(t *testing.T) {
	restoreDefaultTheme(t)
	light, err := ApplyThemeForBackground("forest", "#2AB630", false)
	if err != nil {
		t.Fatal(err)
	}
	if relativeLuminance(mustParseColor(t, light.Background)) < 0.7 {
		t.Fatalf("light terminal background = %s", light.Background)
	}
	if !light.TerminalCanvas {
		t.Fatal("terminal-adapted theme still paints its base canvas")
	}
	if light.Accent != "#2AB630" || contrastRatio(light.Text, light.Surface) < 4.5 {
		t.Fatalf("adapted light theme = %#v", light)
	}
	if light.Green != "#16A34A" || light.Yellow != "#EAB308" || light.Red != "#EF4444" {
		t.Fatalf("light semantic indicators were theme-shifted: %#v", light)
	}
	// An entered accent is rendered as entered, even when adapting the theme to a
	// light terminal would otherwise have lightened it.
	if light.AccentText != "#2AB630" {
		t.Errorf("light accent text = %s, want the entered #2AB630", light.AccentText)
	}

	dark, err := ApplyThemeForBackground("forest", "#4A8C6F", true)
	if err != nil {
		t.Fatal(err)
	}
	if relativeLuminance(mustParseColor(t, dark.Background)) >= 0.2 {
		t.Fatalf("dark terminal background = %s", dark.Background)
	}
	if dark.Accent != "#4A8C6F" || contrastRatio(dark.Text, dark.Surface) < 4.5 {
		t.Fatalf("adapted dark theme = %#v", dark)
	}
}

func TestThemeVariantsCanPaintLightAndDarkCanvases(t *testing.T) {
	restoreDefaultTheme(t)
	for _, name := range ThemeNames() {
		dark, err := ApplyThemeVariant(name, "", "", true, false)
		if err != nil {
			t.Fatal(err)
		}
		light, err := ApplyThemeVariant(name, "", "", false, false)
		if err != nil {
			t.Fatal(err)
		}
		if dark.TerminalCanvas || light.TerminalCanvas {
			t.Errorf("%s painted variant inherited the terminal canvas", name)
		}
		if relativeLuminance(mustParseColor(t, dark.Background)) >= 0.2 {
			t.Errorf("%s dark variant = %s", name, dark.Background)
		}
		if relativeLuminance(mustParseColor(t, light.Background)) < 0.7 {
			t.Errorf("%s light variant = %s", name, light.Background)
		}
		if contrastRatio(dark.Text, dark.Background) < 4.5 || contrastRatio(light.Text, light.Background) < 4.5 {
			t.Errorf("%s variant text contrast is insufficient", name)
		}
	}
}

func TestTerminalBackgroundAdaptsExplicitLightThemesToDark(t *testing.T) {
	backgrounds := make(map[string]bool)
	for _, name := range []string{"github-light", "solarized-light", "cream"} {
		theme, err := ApplyThemeForBackground(name, "", true)
		if err != nil {
			t.Fatal(err)
		}
		if relativeLuminance(mustParseColor(t, theme.Background)) >= 0.2 {
			t.Errorf("%s did not adapt to a dark terminal: %s", name, theme.Background)
		}
		exact, err := ApplyTheme(name, "")
		if err != nil {
			t.Fatal(err)
		}
		if relativeLuminance(mustParseColor(t, exact.Background)) < 0.7 {
			t.Errorf("%s exact theme background is not light: %s", name, exact.Background)
		}
		if exact.TerminalCanvas {
			t.Errorf("%s exact theme unexpectedly inherits the terminal canvas", name)
		}
		backgrounds[theme.Background] = true
	}
	if len(backgrounds) != 3 {
		t.Fatalf("terminal-adapted themes collapsed to %d distinct backgrounds", len(backgrounds))
	}
}

func mustParseColor(t *testing.T, value string) [3]float64 {
	t.Helper()
	color, ok := parseHex(value)
	if !ok {
		t.Fatalf("invalid color %q", value)
	}
	return color
}

func TestAccentOverridesThemeAccent(t *testing.T) {
	restoreDefaultTheme(t)
	theme, err := ApplyTheme("nord", "#ff00aa")
	if err != nil {
		t.Fatal(err)
	}
	if theme.Accent != "#FF00AA" {
		t.Fatalf("accent = %q", theme.Accent)
	}
	if _, err := ApplyTheme("nord", "blue"); err == nil {
		t.Fatal("invalid accent was accepted")
	}
}

func TestThemeStylesPanelTitlesAsSurfaceLabels(t *testing.T) {
	restoreDefaultTheme(t)
	theme, err := ApplyTheme("nord", "")
	if err != nil {
		t.Fatal(err)
	}
	want := lipgloss.Color(theme.Accent)
	for name, got := range map[string]lipgloss.TerminalColor{
		"header":         HeaderStyle.GetForeground(),
		"focused border": FocusedPanelStyle.GetBorderTopForeground(), "modal border": ModalStyle.GetBorderTopForeground(),
	} {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s color = %#v, want %#v", name, got, want)
		}
	}
	if !reflect.DeepEqual(PanelTitleStyle.GetForeground(), lipgloss.Color(theme.Text)) {
		t.Fatalf("panel title color = %#v, want readable text color %s", PanelTitleStyle.GetForeground(), theme.Text)
	}
	if !reflect.DeepEqual(PanelTitleStyle.GetBackground(), lipgloss.Color(theme.SurfaceAlt)) {
		t.Fatalf("panel title background = %#v, want styled surface %s", PanelTitleStyle.GetBackground(), theme.SurfaceAlt)
	}
	focusedBackground := mixHex(theme.SurfaceAlt, theme.Accent, 0.16)
	if !reflect.DeepEqual(FocusedTitleStyle.GetBackground(), lipgloss.Color(focusedBackground)) {
		t.Fatalf("focused panel title background = %#v, want pale accent %s", FocusedTitleStyle.GetBackground(), focusedBackground)
	}
}

func TestLightThemeFocusUsesVeryPaleProjectAccent(t *testing.T) {
	restoreDefaultTheme(t)
	theme, err := ApplyThemeVariant("forest", "#2AB630", "", false, true)
	if err != nil {
		t.Fatal(err)
	}

	want := mixHex(theme.SurfaceAlt, theme.Accent, 0.10)
	if got := FocusedTitleStyle.GetBackground(); !reflect.DeepEqual(got, lipgloss.Color(want)) {
		t.Fatalf("light focused title background = %#v, want %s", got, want)
	}
	if reflect.DeepEqual(FocusedTitleStyle.GetBackground(), lipgloss.Color(theme.Accent)) {
		t.Fatal("focused title uses the saturated project accent instead of a pale tint")
	}
	if !reflect.DeepEqual(PanelTitleStyle.GetBackground(), lipgloss.Color(theme.SurfaceAlt)) {
		t.Fatalf("unfocused title background changed from neutral %s", theme.SurfaceAlt)
	}
	model := &Model{panelFocus: panelServices}
	if got := model.panelTitleStyle(panelServices).GetBackground(); !reflect.DeepEqual(got, FocusedTitleStyle.GetBackground()) {
		t.Fatalf("focused panel did not receive pale accent title: %#v", got)
	}
	if got := model.panelTitleStyle(panelDetails).GetBackground(); !reflect.DeepEqual(got, PanelTitleStyle.GetBackground()) {
		t.Fatalf("unfocused panel did not keep neutral title: %#v", got)
	}
}

func TestPortStyleUsesInfoColor(t *testing.T) {
	restoreDefaultTheme(t)
	theme, err := ApplyTheme("nord", "")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := PortStyle.GetForeground(), lipgloss.Color(theme.Info); !reflect.DeepEqual(got, want) {
		t.Fatalf("port color = %#v, want info color %#v", got, want)
	}
	if reflect.DeepEqual(PortStyle.GetForeground(), lipgloss.Color(theme.Data)) {
		t.Fatal("port color still uses the data/warning palette role")
	}
}

func TestPanelTitlesOverlayTopBorderAndRestoreSurfaceAfterNestedStyles(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)
	restoreDefaultTheme(t)
	if _, err := ApplyTheme("nord", ""); err != nil {
		t.Fatal(err)
	}

	const width = 32
	title := "[3] LOGS" + ContextBarStyle.Render(" │ ") + ServiceNameStyle.Render("api")
	rendered := renderTitledPanel(FocusedPanelStyle, FocusedTitleStyle, width, 3, title, []string{"body"})
	rows := strings.Split(rendered, "\n")
	if got := lipgloss.Width(rows[0]); got != width+2 {
		t.Fatalf("panel top width = %d, want %d", got, width+2)
	}
	plainRows := strings.Split(ansi.Strip(rendered), "\n")
	if !strings.HasPrefix(plainRows[0], "╭─ [3] LOGS │ api ") {
		t.Fatalf("panel title does not overlay its top border: %q", plainRows[0])
	}
	if strings.Contains(plainRows[1], "[3] LOGS") || !strings.Contains(plainRows[1], "body") {
		t.Fatalf("panel title still consumes a content row: %q", plainRows[1])
	}

	prefix := terminalStylePrefix(FocusedTitleStyle)
	const reset = "\x1b[0m"
	label := renderPanelTitle(FocusedTitleStyle, title, width)
	for offset := 0; ; {
		relative := strings.Index(label[offset:], reset)
		if relative < 0 {
			break
		}
		resetEnd := offset + relative + len(reset)
		if resetEnd < len(label) && !strings.HasPrefix(label[resetEnd:], prefix) {
			t.Fatalf("nested reset at byte %d does not restore the title surface", resetEnd-len(reset))
		}
		offset = resetEnd
	}
}

func TestThemeAppliesCanvasAndPanelBackgrounds(t *testing.T) {
	restoreDefaultTheme(t)
	theme, err := ApplyTheme("github-light", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(AppStyle.GetBackground(), lipgloss.Color(theme.Background)) {
		t.Fatalf("canvas background = %#v, want %s", AppStyle.GetBackground(), theme.Background)
	}
	if !reflect.DeepEqual(PanelStyle.GetBackground(), lipgloss.Color(theme.Surface)) {
		t.Fatalf("panel background = %#v, want %s", PanelStyle.GetBackground(), theme.Surface)
	}
	if !reflect.DeepEqual(SelectionStyle.GetBackground(), lipgloss.Color(theme.Selection)) {
		t.Fatalf("selection background = %#v, want %s", SelectionStyle.GetBackground(), theme.Selection)
	}
	for name, foreground := range map[string]lipgloss.TerminalColor{
		"service name": ServiceNameStyle.GetForeground(),
		"log info":     LogInfoStyle.GetForeground(),
		"selection":    SelectionStyle.GetForeground(),
	} {
		if foreground == nil {
			t.Errorf("%s has no explicit foreground", name)
		}
	}
}

func TestStoppedServicesUseDedicatedGrey(t *testing.T) {
	restoreDefaultTheme(t)
	if _, err := ApplyTheme("github-light", ""); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(ColorStopped, ColorGrey) {
		t.Fatal("stopped state collapsed into the normal near-black text color")
	}
	if !reflect.DeepEqual(StoppedBadgeStyle.GetForeground(), ColorStopped) {
		t.Fatal("stopped dot does not use the dedicated grey")
	}
	if !reflect.DeepEqual(ServiceNameStyle.GetForeground(), ColorGrey) {
		t.Fatal("stopped state must not dim the service name")
	}
}

func TestSelectedRowsRestoreTheirOwnBackgroundAfterNestedStyles(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)
	restoreDefaultTheme(t)
	if _, err := ApplyTheme("forest", "#2AB630"); err != nil {
		t.Fatal(err)
	}

	nested := HelpKeyStyle.Render("› ") + ServiceNameStyle.Render("im-widgets")
	rendered := renderSelectedLine(nested)
	prefix := terminalStylePrefix(SelectionStyle)
	if prefix == "" {
		t.Fatal("selection style did not emit an ANSI prefix")
	}
	const reset = "\x1b[0m"
	for offset := 0; ; {
		relative := strings.Index(rendered[offset:], reset)
		if relative < 0 {
			break
		}
		resetEnd := offset + relative + len(reset)
		if resetEnd < len(rendered) && !strings.HasPrefix(rendered[resetEnd:], prefix) {
			t.Fatalf("nested reset at byte %d does not restore selection style", resetEnd-len(reset))
		}
		offset = resetEnd
	}
}

func TestLogRenderingUsesSeparateTimestampAndSourceRoles(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previousProfile)
	restoreDefaultTheme(t)
	theme, err := ApplyTheme("forest", "#2AB630")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(LogInfoStyle.GetForeground(), lipgloss.Color(theme.Text)) {
		t.Fatalf("info log foreground = %#v, want neutral theme text %s", LogInfoStyle.GetForeground(), theme.Text)
	}
	if theme.Text != "#F4F4F5" {
		t.Fatalf("forest primary text = %s, want neutral white #F4F4F5", theme.Text)
	}
	if reflect.DeepEqual(LogInfoStyle.GetForeground(), ColorAccentText) ||
		reflect.DeepEqual(LogInfoStyle.GetForeground(), ColorGreen) {
		t.Fatal("info log foreground inherited the green accent or status color")
	}

	line := "[12:34:56.789] [vite] page reload"
	rendered := styleLogLine(line)
	if ansi.Strip(rendered) != line {
		t.Fatalf("styled log changed its text: %q", ansi.Strip(rendered))
	}
	for name, style := range map[string]lipgloss.Style{
		"timestamp": LogTimestampStyle,
		"source":    LogSourceStyle,
		"message":   LogInfoStyle,
	} {
		if prefix := terminalStylePrefix(style); prefix == "" || !strings.Contains(rendered, prefix) {
			t.Errorf("%s role is missing from rendered log", name)
		}
	}
	if reflect.DeepEqual(LogSourceStyle.GetForeground(), LogInfoStyle.GetForeground()) {
		t.Fatal("ordinary log source is not visually quieter than its message")
	}

	systemLine := "[Kranz] Started · PID 42"
	systemRendered := styleLogLine(systemLine)
	if ansi.Strip(systemRendered) != systemLine {
		t.Fatalf("styled system log changed its text: %q", ansi.Strip(systemRendered))
	}
	if prefix := terminalStylePrefix(LogSystemStyle); prefix == "" || !strings.Contains(systemRendered, prefix) {
		t.Fatal("Kranz system log does not use the dedicated system style")
	}
}
