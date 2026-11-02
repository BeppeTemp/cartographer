package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Icons are generated, not checked in: no asset to review, no generate step.
// Each is an alpha mask of the brand mark (docs/brand/coordinate-*.svg: an
// open C, an inner arc, a separate reference point), anti-aliased by
// supersampling, filled with one colour. Below 32 px the inner arc is dropped,
// as the brand's micro variant does. The state is told by shape (D1482):
//
//	running        the mark
//	starting       the mark without its point, which appears once ready
//	stopped        the mark crossed by a slash, with a gap cut around it
//	not installed  the mark, dimmed
//
// macOS tints a template image itself (PNG, black); Windows has no template
// icons, so its UI renders the same mask white or black from the taskbar theme.

// DimmedAlpha is the opacity of the not-installed mark.
const DimmedAlpha = 0.4

const supersample = 4

// Mark geometry in unit coordinates (y grows downwards), from the 64x64
// viewBox of coordinate.svg: the arc "M50.9 47.9 A26 26 0 1 1 40.9 7.6"
// (stroke 6, butt caps), the inner arc "M42 18 A18 18 0 0 0 17 42" (stroke
// 2.5, round caps) and the point at (55,16) r 5.
const (
	arcX, arcY, arcR       = 0.47983, 0.49249, 26.0 / 64
	arcHalf                = 3.0 / 64      // half the stroke width
	arcFrom, arcTo         = 39.05, 293.08 // degrees, clockwise on screen
	innerX, innerY, innerR = 0.51367, 0.52368, 18.0 / 64
	innerHalf              = 1.25 / 64
	innerFrom, innerTo     = 151.88, 300.46
	dotX, dotY, dotR       = 55.0 / 64, 16.0 / 64, 5.0 / 64
	// minInnerArc is the size from which the inner arc is drawn: the brand's
	// minimum for the standard mark (docs/brand/index.md).
	minInnerArc = 32
	slashHalf   = 0.06 // half the slash width
	slashGap    = 0.05 // clearance cut around the slash
)

// inArc reports whether (x, y) is on the C stroke between arcFrom and to.
// The angle bounds are radial cuts, the butt caps of the SVG.
func inArc(x, y, to float64) bool {
	dx, dy := x-arcX, y-arcY
	if math.Abs(math.Hypot(dx, dy)-arcR) > arcHalf {
		return false
	}
	a := math.Mod(math.Atan2(dy, dx)*180/math.Pi+360, 360)
	return a >= arcFrom && a <= to
}

// inInner reports whether (x, y) is on the inner arc, round caps included.
func inInner(x, y float64) bool {
	dx, dy := x-innerX, y-innerY
	if math.Abs(math.Hypot(dx, dy)-innerR) <= innerHalf {
		a := math.Mod(math.Atan2(dy, dx)*180/math.Pi+360, 360)
		if a >= innerFrom && a <= innerTo {
			return true
		}
	}
	for _, deg := range []float64{innerFrom, innerTo} {
		r := deg * math.Pi / 180
		if math.Hypot(x-innerX-innerR*math.Cos(r), y-innerY-innerR*math.Sin(r)) <= innerHalf {
			return true
		}
	}
	return false
}

func inDot(x, y float64) bool {
	return math.Hypot(x-dotX, y-dotY) <= dotR
}

// slashDist is the distance from the slash's axis, the diagonal from the
// top-left to the bottom-right corner (it clears the point, top-right).
func slashDist(x, y float64) float64 {
	if x < 0.08 || x > 0.92 {
		return math.Inf(1)
	}
	return math.Abs(x-y) / math.Sqrt2
}

func covered(state State, x, y float64, inner bool) float64 {
	body := inArc(x, y, arcTo) || (inner && inInner(x, y))
	mark := body || inDot(x, y)
	switch state {
	case StateRunning:
		if mark {
			return 1
		}
	case StateStarting:
		if body {
			return 1
		}
	case StateStopped:
		d := slashDist(x, y)
		if d <= slashHalf || (mark && d > slashHalf+slashGap) {
			return 1
		}
	case StateNotInstalled:
		if mark {
			return DimmedAlpha
		}
	}
	return 0
}

// Render draws the icon of state at size x size pixels in colour fg. Pixels
// carry fg's RGB and a coverage alpha; fully transparent pixels stay zero.
func Render(state State, size int, fg color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	c := color.NRGBAModel.Convert(fg).(color.NRGBA)
	n := float64(size)
	inner := size >= minInnerArc
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var sum float64
			for sy := 0; sy < supersample; sy++ {
				for sx := 0; sx < supersample; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/supersample) / n
					y := (float64(py) + (float64(sy)+0.5)/supersample) / n
					sum += covered(state, x, y, inner)
				}
			}
			a := sum / (supersample * supersample) * float64(c.A) / 255
			if a <= 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{R: c.R, G: c.G, B: c.B, A: uint8(math.Round(a * 255))})
		}
	}
	return img
}

// PNG is the black icon of state as PNG bytes, the macOS template image.
func PNG(state State, size int) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, Render(state, size, color.Black)); err != nil {
		return nil // an in-memory encode of an NRGBA image cannot fail
	}
	return buf.Bytes()
}
