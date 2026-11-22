package ui

import (
	"math"
	"testing"

	"github.com/lucasb-eyer/go-colorful"
)

func TestDependencyGradientRotatesAroundGreenAccent(t *testing.T) {
	const accent = "#63D392"
	for _, surface := range []string{"#202630", "#FAF9F5"} {
		t.Run(surface, func(t *testing.T) {
			base, err := colorful.Hex(accent)
			if err != nil {
				t.Fatal(err)
			}
			baseHue, _, _ := base.Hsl()
			for _, position := range []float64{-1, 0, 1} {
				shade := dependencyAccentShade(accent, surface, position)
				color, err := colorful.Hex(shade)
				if err != nil {
					t.Fatal(err)
				}
				hue, _, _ := color.Hsl()
				if want := baseHue + position*dependencyHueSpan; math.Abs(hue-want) > 2 {
					t.Errorf("shade at %g has hue %.1f, want %.1f", position, hue, want)
				}
				if ratio := contrastRatio(shade, surface); ratio < 4.5 {
					t.Errorf("shade at %g has contrast %.2f, want at least 4.5", position, ratio)
				}
			}
		})
	}
}

func TestDependencyGradientKeepsGrayAccentGray(t *testing.T) {
	warm := dependencyAccentShade("#ADADAD", "#202630", -1)
	cool := dependencyAccentShade("#ADADAD", "#202630", 1)
	for _, shade := range []string{warm, cool} {
		color, err := colorful.Hex(shade)
		if err != nil {
			t.Fatal(err)
		}
		if _, saturation, _ := color.Hsl(); saturation != 0 {
			t.Errorf("gray accent produced a tinted shade %s", shade)
		}
	}
	if warm == cool {
		t.Fatal("gray accent produced identical endpoint shades")
	}
}

func TestDependencyGradientStaysNearDefaultCyan(t *testing.T) {
	const accent = "#38BDF8"
	const surface = "#202630"
	for _, position := range []float64{-1, 0, 1} {
		shade, err := colorful.Hex(dependencyAccentShade(accent, surface, position))
		if err != nil {
			t.Fatal(err)
		}
		hue, _, _ := shade.Hsl()
		if hue < 180 || hue > 220 {
			t.Errorf("cyan accent produced hue %.1f at %g, want a shade between cyan and blue", hue, position)
		}
	}
}
