package ui

import (
	"math"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/termenv"
)

const dependencyHueSpan = 14.0

func dependencyDepthStyle(renderer *lipgloss.Renderer, depth, count int) lipgloss.Style {
	style := renderer.NewStyle()
	profile := renderer.ColorProfile()
	if profile == termenv.Ascii {
		return style
	}
	progress := 0.5
	if count > 1 {
		progress = float64(depth) / float64(count-1)
	}
	accent, surface := string(ColorAccentText), string(ColorSurfaceAlt)
	shade := dependencyAccentShade(accent, surface, 2*progress-1)
	return style.Foreground(lipgloss.Color(shade))
}

// dependencyAccentShade traverses the shorter hue arc around the accent.
// On a gray theme the same position changes lightness instead of introducing
// a hue. Contrast adjustments preserve the direction of the hue shift.
func dependencyAccentShade(accent, surface string, position float64) string {
	accent = ensureContrast(accent, surface, 4.5)
	color, err := colorful.Hex(accent)
	if err != nil {
		return ensureContrast("#808080", surface, 4.5)
	}
	hue, saturation, lightness := color.Hsl()
	if saturation < 0.04 {
		lightness = max(0, min(1, lightness+position*0.14))
		return ensureContrast(colorful.Hsl(0, 0, lightness).Hex(), surface, 4.5)
	}
	hue = math.Mod(hue+360+position*dependencyHueSpan, 360)
	lightness = max(0, min(1, lightness+position*0.035))
	return ensureContrast(colorful.Hsl(hue, saturation, lightness).Hex(), surface, 4.5)
}
