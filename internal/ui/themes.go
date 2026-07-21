package ui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// DefaultTheme is used when neither project nor user settings select a theme.
const DefaultTheme = "kranz"

// Theme is a contrast-oriented terminal palette applied to the complete canvas.
type Theme struct {
	Name        string
	DisplayName string
	Accent      string
	Green       string
	Yellow      string
	Red         string
	Text        string
	Muted       string
	Border      string
	Background  string
	Surface     string
	SurfaceAlt  string
	AccentText  string
	Info        string
	Data        string
	Selection   string
	SelectText  string
	// TerminalCanvas leaves the base canvas unpainted so the terminal profile
	// supplies its exact background color. Derived colors still use Background
	// as a light/dark contrast reference.
	TerminalCanvas bool
}

var themeOrder = []string{
	"kranz", "tokyo-night", "dracula", "nord", "gruvbox-dark", "catppuccin-mocha",
	"rose-pine", "solarized-dark", "monokai", "everforest", "one-dark", "github-dark",
	"ocean", "forest", "amber", "high-contrast",
	"github-light", "solarized-light", "cream",
}

var themes = map[string]Theme{
	"kranz":            {Name: "kranz", DisplayName: "Kranz Cyan", Accent: "#38BDF8", Green: "#4ADE80", Yellow: "#FBBF24", Red: "#FB7185", Text: "#D8E0EA", Muted: "#8D99A8", Border: "#3B4654", Surface: "#151A21"},
	"tokyo-night":      {Name: "tokyo-night", DisplayName: "Tokyo Night", Accent: "#7AA2F7", Green: "#9ECE6A", Yellow: "#E0AF68", Red: "#F7768E", Text: "#C0CAF5", Muted: "#7F849C", Border: "#3B4261", Surface: "#1A1B26"},
	"dracula":          {Name: "dracula", DisplayName: "Dracula", Accent: "#BD93F9", Green: "#50FA7B", Yellow: "#F1FA8C", Red: "#FF5555", Text: "#F8F8F2", Muted: "#8A88A8", Border: "#44475A", Surface: "#282A36"},
	"nord":             {Name: "nord", DisplayName: "Nord", Accent: "#88C0D0", Green: "#A3BE8C", Yellow: "#EBCB8B", Red: "#BF616A", Text: "#ECEFF4", Muted: "#8B9AAF", Border: "#4C566A", Surface: "#2E3440"},
	"gruvbox-dark":     {Name: "gruvbox-dark", DisplayName: "Gruvbox Dark", Accent: "#FABD2F", Green: "#B8BB26", Yellow: "#FABD2F", Red: "#FB4934", Text: "#EBDBB2", Muted: "#A89984", Border: "#665C54", Surface: "#282828"},
	"catppuccin-mocha": {Name: "catppuccin-mocha", DisplayName: "Catppuccin Mocha", Accent: "#CBA6F7", Green: "#A6E3A1", Yellow: "#F9E2AF", Red: "#F38BA8", Text: "#CDD6F4", Muted: "#9399B2", Border: "#45475A", Surface: "#1E1E2E"},
	"rose-pine":        {Name: "rose-pine", DisplayName: "Rosé Pine", Accent: "#EB6F92", Green: "#9CCFD8", Yellow: "#F6C177", Red: "#EB6F92", Text: "#E0DEF4", Muted: "#908CAA", Border: "#403D52", Surface: "#191724"},
	"solarized-dark":   {Name: "solarized-dark", DisplayName: "Solarized Dark", Accent: "#2AA198", Green: "#859900", Yellow: "#B58900", Red: "#DC322F", Text: "#EEE8D5", Muted: "#839496", Border: "#586E75", Surface: "#002B36"},
	"monokai":          {Name: "monokai", DisplayName: "Monokai", Accent: "#F92672", Green: "#A6E22E", Yellow: "#E6DB74", Red: "#F92672", Text: "#F8F8F2", Muted: "#A59F85", Border: "#75715E", Surface: "#272822"},
	"everforest":       {Name: "everforest", DisplayName: "Everforest", Accent: "#A7C080", Green: "#A7C080", Yellow: "#DBBC7F", Red: "#E67E80", Text: "#D3C6AA", Muted: "#859289", Border: "#4F5B58", Surface: "#2D353B"},
	"one-dark":         {Name: "one-dark", DisplayName: "One Dark", Accent: "#61AFEF", Green: "#98C379", Yellow: "#E5C07B", Red: "#E5747D", Text: "#E6E6E6", Muted: "#ABB2BF", Border: "#4B5363", Surface: "#282C34"},
	"github-dark":      {Name: "github-dark", DisplayName: "GitHub Dark", Accent: "#58A6FF", Green: "#56D364", Yellow: "#D29922", Red: "#FF7B72", Text: "#E6EDF3", Muted: "#9DA7B3", Border: "#3D444D", Surface: "#0D1117"},
	"ocean":            {Name: "ocean", DisplayName: "Ocean", Accent: "#31C5F4", Green: "#5DE4A8", Yellow: "#FFD166", Red: "#FF7892", Text: "#EAF8FF", Muted: "#A7C2D4", Border: "#315267", Surface: "#08131D"},
	"forest":           {Name: "forest", DisplayName: "Forest", Accent: "#63D392", Green: "#76E39F", Yellow: "#FFD166", Red: "#FF7D87", Text: "#E8EFEA", Muted: "#A8B3AC", Border: "#46534A", Surface: "#171C19"},
	"amber":            {Name: "amber", DisplayName: "Amber Terminal", Accent: "#FFB000", Green: "#7FD962", Yellow: "#FFD166", Red: "#FF5C57", Text: "#FFE7B3", Muted: "#B89B63", Border: "#6B4E16", Surface: "#1C1408"},
	"high-contrast":    {Name: "high-contrast", DisplayName: "High Contrast", Accent: "#00FFFF", Green: "#55FF55", Yellow: "#FFFF00", Red: "#FF5555", Text: "#FFFFFF", Muted: "#FFFFFF", Border: "#FFFFFF", Background: "#000000", Surface: "#000000", SurfaceAlt: "#000000"},
	"github-light":     {Name: "github-light", DisplayName: "GitHub Light", Accent: "#0969DA", Green: "#1A7F37", Yellow: "#9A6700", Red: "#CF222E", Text: "#1F2328", Muted: "#59636E", Border: "#A8B3BF", Background: "#F6F8FA", Surface: "#F6F8FA", SurfaceAlt: "#EAEEF2"},
	"solarized-light":  {Name: "solarized-light", DisplayName: "Solarized Light", Accent: "#006DAD", Green: "#657B00", Yellow: "#8A6500", Red: "#C52B32", Text: "#073642", Muted: "#4F6268", Border: "#93A1A1", Background: "#FDF6E3", Surface: "#FDF6E3", SurfaceAlt: "#EEE8D5"},
	"cream":            {Name: "cream", DisplayName: "Warm Cream", Accent: "#9A4B00", Green: "#287A47", Yellow: "#8A5A00", Red: "#B4232E", Text: "#292524", Muted: "#655E57", Border: "#B9AA96", Background: "#F5EFE3", Surface: "#F5EFE3", SurfaceAlt: "#EDE3D2"},
}

var hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func init() {
	_, _ = ApplyTheme(DefaultTheme, "")
}

// ThemeNames returns themes in the stable order used by the picker.
func ThemeNames() []string {
	return append([]string(nil), themeOrder...)
}

// LookupTheme returns a named built-in theme.
func LookupTheme(name string) (Theme, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == "default" {
		name = DefaultTheme
	}
	theme, ok := themes[name]
	if ok {
		theme = normalizeTheme(theme)
	}
	return theme, ok
}

func normalizeTheme(theme Theme) Theme {
	if theme.Background == "" {
		theme.Background = theme.Surface
	}
	// The dashboard uses one continuous canvas so terminal resets cannot leave
	// random patches inside panels. SurfaceAlt is reserved for intentional,
	// bounded elevation such as modals and selected rows.
	theme.Surface = theme.Background
	if theme.SurfaceAlt == "" {
		theme.SurfaceAlt = mixHex(theme.Background, theme.Text, 0.07)
	}
	theme.Green, theme.Yellow, theme.Red = semanticStatusColors(theme.Background)
	theme.AccentText = ensureContrast(theme.Accent, theme.Surface, 4.5)
	if theme.Info == "" {
		theme.Info = adaptiveInfoColor(theme.Background)
	}
	theme.Info = ensureContrast(theme.Info, theme.Surface, 4.5)
	if theme.Data == "" {
		theme.Data = theme.Yellow
	}
	theme.Data = ensureContrast(theme.Data, theme.Surface, 4.5)
	if theme.Selection == "" {
		blend := 0.28
		if background, ok := parseHex(theme.Background); ok && relativeLuminance(background) > 0.45 {
			blend = 0.15
		}
		theme.Selection = mixHex(theme.Background, theme.Info, blend)
	}
	theme.SelectText = ensureContrast(theme.Text, theme.Selection, 4.5)
	return theme
}

func adaptiveInfoColor(background string) string {
	if color, ok := parseHex(background); ok && relativeLuminance(color) > 0.45 {
		return "#0969DA"
	}
	return "#7DB7FF"
}

func semanticStatusColors(background string) (green, yellow, red string) {
	color, ok := parseHex(background)
	if ok && relativeLuminance(color) > 0.45 {
		// Saturated indicator colors remain recognisable on a light canvas. They
		// intentionally do not get darkened into theme-specific brown/black text.
		return "#16A34A", "#EAB308", "#EF4444"
	}
	return "#4ADE80", "#FACC15", "#FB7185"
}

// ApplyTheme activates a built-in theme and an optional #RRGGBB accent.
func ApplyTheme(name, accent string) (Theme, error) {
	theme, err := resolveTheme(name, accent)
	if err != nil {
		return Theme{}, err
	}
	theme = normalizeTheme(theme)
	applyPalette(theme)
	return theme, nil
}

// ApplyThemeForBackground adapts palette surfaces and semantic colors to the
// terminal's detected light/dark mode while preserving the theme identity and
// project accent.
func ApplyThemeForBackground(name, accent string, darkBackground bool) (Theme, error) {
	return ApplyThemeVariant(name, accent, darkBackground, true)
}

// ApplyThemeVariant selects a light or dark variant independently from who
// paints the canvas. Terminal-owned canvases inherit the profile background;
// theme-owned canvases paint the adapted surface themselves.
func ApplyThemeVariant(name, accent string, dark, terminalCanvas bool) (Theme, error) {
	theme, err := resolveTheme(name, accent)
	if err != nil {
		return Theme{}, err
	}
	theme = adaptThemeBackground(theme, dark)
	theme.TerminalCanvas = terminalCanvas
	applyPalette(theme)
	return theme, nil
}

func resolveTheme(name, accent string) (Theme, error) {
	theme, ok := LookupTheme(name)
	if !ok {
		return Theme{}, fmt.Errorf("unknown theme %q (available: %s)", name, strings.Join(themeOrder, ", "))
	}
	accent = strings.TrimSpace(accent)
	if accent == "" {
		return theme, nil
	}
	if !hexColorPattern.MatchString(accent) {
		return Theme{}, fmt.Errorf("accent must use #RRGGBB format, got %q", accent)
	}
	theme.Accent = strings.ToUpper(accent)
	return theme, nil
}

func adaptThemeBackground(theme Theme, dark bool) Theme {
	background, ok := parseHex(theme.Background)
	themeIsDark := !ok || relativeLuminance(background) <= 0.45
	if dark == themeIsDark {
		return normalizeTheme(theme)
	}
	if dark {
		theme.Background, theme.SurfaceAlt = adaptiveDarkSurfaces(theme.Name)
		theme.Text = "#E6EDF3"
		theme.Muted = "#9DA7B3"
		theme.Border = "#3D4855"
	} else {
		theme.Background, theme.SurfaceAlt = adaptiveLightSurfaces(theme.Name)
		theme.Text = "#1F2328"
		theme.Muted = "#59636E"
		theme.Border = "#A8B3BF"
	}
	theme.Surface = theme.Background
	theme.Info = ""
	theme.Data = ""
	theme.Selection = ""
	theme.SelectText = ""
	return normalizeTheme(theme)
}

func adaptiveLightSurfaces(name string) (background, alternate string) {
	switch name {
	case "dracula", "catppuccin-mocha":
		return "#F8F5FC", "#EEE8F5"
	case "nord", "ocean", "kranz":
		return "#F1F7FA", "#E5EFF4"
	case "gruvbox-dark", "monokai", "amber":
		return "#FBF4E8", "#F2E7D3"
	case "rose-pine":
		return "#FAF3F5", "#F2E6EA"
	case "solarized-dark":
		return "#FDF6E3", "#EEE8D5"
	case "everforest", "forest":
		return "#F2F7F3", "#E5EFE7"
	default:
		return "#F6F8FA", "#EAEEF2"
	}
}

func adaptiveDarkSurfaces(name string) (background, alternate string) {
	switch name {
	case "solarized-light":
		return "#002B36", "#073642"
	case "cream":
		return "#231F1A", "#302A23"
	case "github-light":
		return "#161B22", "#21262D"
	default:
		return "#151A21", "#202630"
	}
}

func ensureContrast(foreground, background string, minimum float64) string {
	if contrastRatio(foreground, background) >= minimum {
		return strings.ToUpper(foreground)
	}
	backgroundRGB, ok := parseHex(background)
	if !ok {
		return strings.ToUpper(foreground)
	}
	target := [3]float64{255, 255, 255}
	if relativeLuminance(backgroundRGB) > 0.45 {
		target = [3]float64{0, 0, 0}
	}
	start, ok := parseHex(foreground)
	if !ok {
		return strings.ToUpper(foreground)
	}
	for step := 1; step <= 20; step++ {
		amount := float64(step) / 20
		candidate := [3]float64{
			start[0] + (target[0]-start[0])*amount,
			start[1] + (target[1]-start[1])*amount,
			start[2] + (target[2]-start[2])*amount,
		}
		hex := formatHex(candidate)
		if contrastRatio(hex, background) >= minimum {
			return hex
		}
	}
	return formatHex(target)
}

func contrastRatio(a, b string) float64 {
	first, okFirst := parseHex(a)
	second, okSecond := parseHex(b)
	if !okFirst || !okSecond {
		return 1
	}
	lighter, darker := relativeLuminance(first), relativeLuminance(second)
	if lighter < darker {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

func parseHex(value string) ([3]float64, bool) {
	var result [3]float64
	if !hexColorPattern.MatchString(value) {
		return result, false
	}
	for index := range 3 {
		parsed, err := strconv.ParseUint(value[1+index*2:3+index*2], 16, 8)
		if err != nil {
			return result, false
		}
		result[index] = float64(parsed)
	}
	return result, true
}

func relativeLuminance(color [3]float64) float64 {
	linear := func(component float64) float64 {
		component /= 255
		if component <= 0.04045 {
			return component / 12.92
		}
		return math.Pow((component+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(color[0]) + 0.7152*linear(color[1]) + 0.0722*linear(color[2])
}

func formatHex(color [3]float64) string {
	return fmt.Sprintf("#%02X%02X%02X", int(math.Round(color[0])), int(math.Round(color[1])), int(math.Round(color[2])))
}

func mixHex(base, overlay string, overlayAmount float64) string {
	baseColor, baseOK := parseHex(base)
	overlayColor, overlayOK := parseHex(overlay)
	if !baseOK || !overlayOK {
		return strings.ToUpper(base)
	}
	overlayAmount = math.Max(0, math.Min(1, overlayAmount))
	return formatHex([3]float64{
		baseColor[0] + (overlayColor[0]-baseColor[0])*overlayAmount,
		baseColor[1] + (overlayColor[1]-baseColor[1])*overlayAmount,
		baseColor[2] + (overlayColor[2]-baseColor[2])*overlayAmount,
	})
}
