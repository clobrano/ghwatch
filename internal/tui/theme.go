package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// flavor is a Catppuccin palette (https://catppuccin.com/palette), with
// the colors ghwatch uses.
type flavor struct {
	text, subtext1, subtext0, overlay1, surface2, surface1, surface0, base, mantle string
	blue, lavender, sapphire, red, green, yellow, peach, mauve                     string
}

// Themes are the Catppuccin flavors, by the name config.toml uses.
var themes = map[string]flavor{
	"latte": {
		text: "4c4f69", subtext1: "5c5f77", subtext0: "6c6f85", overlay1: "8c8fa1",
		surface2: "acb0be", surface1: "bcc0cc", surface0: "ccd0da", base: "eff1f5", mantle: "e6e9ef",
		blue: "1e66f5", lavender: "7287fd", sapphire: "209fb5", red: "d20f39", green: "40a02b",
		yellow: "df8e1d", peach: "fe640b", mauve: "8839ef",
	},
	"frappe": {
		text: "c6d0f5", subtext1: "b5bfe2", subtext0: "a5adce", overlay1: "838ba7",
		surface2: "626880", surface1: "51576d", surface0: "414559", base: "303446", mantle: "292c3c",
		blue: "8caaee", lavender: "babbf1", sapphire: "85c1dc", red: "e78284", green: "a6d189",
		yellow: "e5c890", peach: "ef9f76", mauve: "ca9ee6",
	},
	"macchiato": {
		text: "cad3f5", subtext1: "b8c0e0", subtext0: "a5adcb", overlay1: "8087a2",
		surface2: "5b6078", surface1: "494d64", surface0: "363a4f", base: "24273a", mantle: "1e2030",
		blue: "8aadf4", lavender: "b7bdf8", sapphire: "7dc4e4", red: "ed8796", green: "a6da95",
		yellow: "eed49f", peach: "f5a97f", mauve: "c6a0f6",
	},
	"mocha": {
		text: "cdd6f4", subtext1: "bac2de", subtext0: "a6adc8", overlay1: "7f849c",
		surface2: "585b70", surface1: "45475a", surface0: "313244", base: "1e1e2e", mantle: "181825",
		blue: "89b4fa", lavender: "b4befe", sapphire: "74c7ec", red: "f38ba8", green: "a6e3a1",
		yellow: "f9e2af", peach: "fab387", mauve: "cba6f7",
	},
}

// theme is the flavor in use.
var theme flavor

// DefaultTheme is the flavor used when config.toml names none.
const DefaultTheme = "frappe"

// ThemeNames lists the themes, sorted.
func ThemeNames() []string {
	names := make([]string, 0, len(themes))
	for n := range themes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// UseTheme sets the colors of every style to a Catppuccin flavor. The
// terminal's own background stays: pick the flavor that matches it.
func UseTheme(name string) error {
	f, ok := themes[name]
	if !ok {
		return fmt.Errorf("unknown theme %q (want %s)", name, strings.Join(ThemeNames(), ", "))
	}
	theme = f
	// Content (text, details, counts, states) uses colors readable on the
	// flavor's base; Overlay 1 and Surface 2 are for decoration only.
	sText, sStrong = fg(f.text), fg(f.text)
	sMuted, sSecondary = fg(f.subtext1), fg(f.subtext0)
	sDim, sBorder = fg(f.overlay1), fg(f.surface2)
	sAccent, sCyan, sPanelBorder = fg(f.blue), fg(f.blue), fg(f.lavender)
	sRed, sGreen, sYellow = fg(f.red), fg(f.green), fg(f.yellow)
	sBlue, sMagenta, sNew = fg(f.sapphire), fg(f.mauve), fg(f.peach)
	sOnAccent = fg(f.base)
	bgAccent, bgSelect, bgSoft, bgOverlay = bg(f.blue), bg(f.surface1), bg(f.surface0), bg(f.mantle)
	return nil
}

func init() {
	if err := UseTheme(DefaultTheme); err != nil {
		panic(err)
	}
}

// minContrast is the contrast colored text keeps on the background:
// WCAG's 4.5:1 for normal text.
const minContrast = 4.5

func validHex(hex string) bool {
	var r, g, b int
	n, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return err == nil && n == 3 && len(hex) == 6
}

func parseHex(hex string) [3]float64 {
	var c [3]int
	fmt.Sscanf(hex, "%02x%02x%02x", &c[0], &c[1], &c[2])
	return [3]float64{float64(c[0]), float64(c[1]), float64(c[2])}
}

// luminance is a color's WCAG relative luminance.
func luminance(c [3]float64) float64 {
	var l float64
	for i, w := range []float64{0.2126, 0.7152, 0.0722} {
		v := c[i] / 255
		if v <= 0.03928 {
			v /= 12.92
		} else {
			v = math.Pow((v+0.055)/1.055, 2.4)
		}
		l += w * v
	}
	return l
}

// contrast is the WCAG contrast ratio of two hex colors.
func contrast(a, b string) float64 {
	la, lb := luminance(parseHex(a)), luminance(parseHex(b))
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// readable returns hex, mixed with white on a dark background (black on
// a light one) in small steps until it reaches minContrast on bg.
func readable(hex, bg string) string {
	target := 255.0
	if luminance(parseHex(bg)) > 0.5 {
		target = 0
	}
	c := parseHex(hex)
	out := hex
	for step := 0; step <= 20 && contrast(out, bg) < minContrast; step++ {
		k := float64(step) / 20
		var m [3]int
		for i := range c {
			m[i] = int(math.Round(c[i] + (target-c[i])*k))
		}
		out = fmt.Sprintf("%02x%02x%02x", m[0], m[1], m[2])
	}
	return out
}
