package branding

import (
	"math"
	"sort"
	"strconv"
)

// Thresholds of the audited pairs and the fixed surfaces of the two themes.
// Keep in step with web/src/test/contrast.test.ts (the formula and the
// thresholds) and web/src/styles/tokens.css (the surfaces): derive_test.go
// reads tokens.css and fails when a surface here drifts from it.
const (
	textMin    = 4.5 // brand colours that appear as text, text on a brand fill
	graphicMin = 3.0 // a graphical object on surface-0
)

var (
	darkPanels  = [3]string{"#131416", "#1a1b1e", "#222327"}
	lightPanels = [3]string{"#f5f2eb", "#fcfaf6", "#e9e8de"}
	// textOn are the two --text-on-* values of the shipped themes (dark, light).
	textOn = [2]string{"#131416", "#fcfaf6"}
)

// luminance is the relative luminance of a #rrggbb colour: a port of
// web/src/test/contrast.test.ts luminance(), same constants.
func luminance(hex string) float64 {
	ch := func(off int) float64 {
		v, _ := strconv.ParseUint(hex[off:off+2], 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(1) + 0.7152*ch(3) + 0.0722*ch(5)
}

// ratio is the contrast ratio of two colours, (hi+0.05)/(lo+0.05).
func ratio(a, b string) float64 {
	l := []float64{luminance(a), luminance(b)}
	sort.Float64s(l)
	return (l[1] + 0.05) / (l[0] + 0.05)
}
