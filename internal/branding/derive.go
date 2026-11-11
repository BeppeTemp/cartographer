package branding

import (
	"fmt"
	"math"
	"strconv"
)

// maxShift is how many points of lightness an accent may move per theme to
// pass; beyond it the accent is refused with the nearest passing colour.
const maxShift = 45

// Theme is the accent-dependent tokens of one theme.
type Theme struct {
	Accent, Primary, PrimaryDim, TextOnPrimary, TextOnAccent, EdgeActive string
	// Light only: --graph-edge-3d-active, --graph-ring, --graph-glow-a.
	EdgeActive3D, Ring, Glow string
	// Adjusted says the lightness moved by Shift points from From.
	Adjusted bool
	Shift    int
	From     string
	// PanelRatio is the weakest ratio of the accent on the three panels,
	// TextRatio that of the text colour on the accent: what the preview shows.
	PanelRatio, TextRatio float64
}

// Palette is both themes derived from one accent.
type Palette struct{ Dark, Light Theme }

// Derive turns an accent (#rrggbb, lowercase) into the tokens of both themes,
// each verified against the audited pairs. It errors when either theme needs
// more than maxShift points of lightness, naming the nearest passing colour.
func Derive(accent string) (Palette, error) {
	var p Palette
	var err error
	if p.Dark, err = deriveTheme(accent, darkPanels, +1, "dark"); err != nil {
		return Palette{}, err
	}
	if p.Light, err = deriveTheme(accent, lightPanels, -1, "light"); err != nil {
		return Palette{}, err
	}
	p.Dark.EdgeActive = p.Dark.Primary
	l := p.Light
	r, g, b := rgb(l.Accent)
	p.Light.EdgeActive, p.Light.EdgeActive3D, p.Light.Ring = l.Accent, l.Accent, l.Accent
	p.Light.Glow = fmt.Sprintf("rgb(%d %d %d / 0.06)", r, g, b)
	return p, nil
}

// pairs reports whether a colour holds every accent-dependent pair on a theme's
// panels, and the text colour that sits on it; the ratios are for the preview.
func pairs(c string, panels [3]string, prefer int) (text string, panel, onText float64, ok bool) {
	panel = math.MaxFloat64
	for _, p := range panels {
		panel = math.Min(panel, ratio(c, p))
	}
	if panel < textMin || ratio(c, panels[0]) < graphicMin {
		return "", panel, 0, false
	}
	for _, t := range []string{textOn[prefer], textOn[1-prefer]} {
		if r := ratio(t, c); r >= textMin {
			return t, panel, r, true
		}
	}
	return "", panel, 0, false
}

// deriveTheme moves the lightness in dir (+1 lighter, -1 darker) one point at a
// time, keeping hue and saturation, until every pair holds.
func deriveTheme(accent string, panels [3]string, dir int, name string) (Theme, error) {
	prefer := 0
	if dir < 0 {
		prefer = 1
	}
	h, s, l := toHSL(accent)
	cand := func(k int) string {
		if k == 0 {
			return accent
		}
		return fromHSL(h, s, l+float64(dir*k)/100)
	}
	for k := 0; k <= maxShift; k++ {
		c := cand(k)
		text, panel, onText, ok := pairs(c, panels, prefer)
		if !ok {
			continue
		}
		t := Theme{Accent: c, Primary: c, PrimaryDim: c, TextOnPrimary: text, TextOnAccent: text,
			Adjusted: k > 0, Shift: k, From: accent, PanelRatio: panel, TextRatio: onText}
		// The dim shade is 6 points darker on both themes, like the shipped palette,
		// and stays the accent itself when the shade fails the same pairs or the
		// text on it: .button--primary:hover draws --text-on-primary over --primary-dim
		// (web/src/styles/base.css), so that pair is audited too.
		if dim := fromHSL(h, s, lightOf(c)-0.06); dim != c {
			if _, _, _, ok := pairs(dim, panels, prefer); ok && ratio(text, dim) >= textMin {
				t.PrimaryDim = dim
			}
		}
		return t, nil
	}
	near := ""
	for k := maxShift + 1; k <= 100; k++ {
		if _, _, _, ok := pairs(cand(k), panels, prefer); ok {
			near = cand(k)
			break
		}
	}
	want := map[int]string{+1: "lighter", -1: "darker"}[dir]
	msg := fmt.Sprintf("accent %s: cannot be shown on the %s theme within %d points of lightness", accent, name, maxShift)
	if near != "" {
		msg += fmt.Sprintf(" (the nearest colour that passes is %s)", near)
	}
	return Theme{}, fmt.Errorf("%s; pick a %s colour", msg, want)
}

func lightOf(hex string) float64 { _, _, l := toHSL(hex); return l }

func rgb(hex string) (r, g, b int) {
	v, _ := strconv.ParseUint(hex[1:], 16, 32)
	return int(v >> 16 & 255), int(v >> 8 & 255), int(v & 255)
}

// toHSL converts #rrggbb to hue (0-6), saturation and lightness (0-1).
func toHSL(hex string) (h, s, l float64) {
	ri, gi, bi := rgb(hex)
	r, g, b := float64(ri)/255, float64(gi)/255, float64(bi)/255
	hi, lo := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (hi + lo) / 2
	d := hi - lo
	if d == 0 {
		return 0, 0, l
	}
	s = d / (1 - math.Abs(2*l-1))
	switch hi {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	if h < 0 {
		h += 6
	}
	return h, s, l
}

// fromHSL is toHSL's inverse, clamping the lightness, rounded to a lowercase hex.
func fromHSL(h, s, l float64) string {
	l = math.Max(0, math.Min(1, l))
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch int(h) % 6 {
	case 0:
		r, g, b = c, x, 0
	case 1:
		r, g, b = x, c, 0
	case 2:
		r, g, b = 0, c, x
	case 3:
		r, g, b = 0, x, c
	case 4:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	q := func(v float64) int { return int(math.Round((v + m) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", q(r), q(g), q(b))
}
