package tray

import (
	"image/color"
	"math"
	"testing"
)

// luminance is the WCAG relative luminance of an opaque sRGB colour, the same
// formula as web/src/test/contrast.test.ts.
func luminance(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func contrast(a, b color.NRGBA) float64 {
	hi, lo := luminance(a), luminance(b)
	if lo > hi {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05)
}

// Every tone with a fixed colour (every entry of toneTable, so a tone added
// later is checked too) is readable (WCAG AA, 4.5:1) on the light and on the
// dark menu: the orange "Out of sync" was not on the light one. ToneNone and
// ToneIdle use the system label colour and have no fixed one.
func TestToneColorsAreReadableOnTheMenu(t *testing.T) {
	const minRatio = 4.5
	for _, tone := range []Tone{ToneNone, ToneIdle} {
		if _, ok := toneColor(tone, false); ok {
			t.Errorf("tone %d has a fixed colour (light), want the system label colour", tone)
		}
		if _, ok := toneColor(tone, true); ok {
			t.Errorf("tone %d has a fixed colour (dark), want the system label colour", tone)
		}
	}
	if len(toneTable) == 0 {
		t.Fatal("toneTable is empty")
	}
	for tone := range toneTable {
		for _, ap := range []struct {
			name string
			dark bool
			bgs  []color.NRGBA
		}{{"light", false, menuLight}, {"dark", true, menuDark}} {
			fg, ok := toneColor(tone, ap.dark)
			if !ok {
				t.Errorf("tone %d has no fixed colour on the %s menu", tone, ap.name)
				continue
			}
			for _, bg := range ap.bgs {
				if r := contrast(fg, bg); r < minRatio {
					t.Errorf("tone %d on the %s menu: #%02X%02X%02X on #%02X%02X%02X is %.2f:1, want >= %.1f",
						tone, ap.name, fg.R, fg.G, fg.B, bg.R, bg.G, bg.B, r, minRatio)
				}
			}
		}
	}
}
