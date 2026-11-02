package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Icons are generated, not checked in: no asset to review, no generate step.
// Each is an alpha mask of a map pin, anti-aliased by supersampling, filled
// with one colour. The state is told by shape (D1482):
//
//	running        filled pin
//	starting       outlined pin, left half of the head filled
//	stopped        outlined pin crossed by a diagonal slash
//	not installed  outlined pin, dimmed
//
// macOS tints a template image itself (PNG, black); Windows has no template
// icons, so its UI renders the same mask white or black from the taskbar theme.

// DimmedAlpha is the opacity of the not-installed pin.
const DimmedAlpha = 0.4

const supersample = 4

// Pin geometry in unit coordinates (y grows downwards).
const (
	headX, headY, headR = 0.5, 0.38, 0.30
	tipY                = 0.93
	baseY               = 0.50
	baseHalf            = 0.26
	stroke              = 0.11 // outline thickness
)

func inPin(x, y, inset float64) bool {
	dx, dy := x-headX, y-headY
	if dx*dx+dy*dy <= (headR-inset)*(headR-inset) {
		return true
	}
	// Triangle from the base chord to the tip, inset along its edges.
	if y < baseY || y > tipY-inset*2.4 {
		return false
	}
	t := (y - baseY) / (tipY - baseY) // 0 at the base, 1 at the tip
	half := (baseHalf - inset*1.2) * (1 - t)
	return math.Abs(x-headX) <= half
}

// inSlash is a band around the anti-diagonal x + y = 1.
func inSlash(x, y float64) bool {
	return math.Abs(x+y-1.0)/math.Sqrt2 <= 0.065 && x > 0.1 && x < 0.9
}

func covered(state State, x, y float64) float64 {
	outline := inPin(x, y, 0) && !inPin(x, y, stroke)
	switch state {
	case StateRunning:
		if inPin(x, y, 0) {
			return 1
		}
	case StateStarting:
		if outline || (x < headX && math.Hypot(x-headX, y-headY) <= headR) {
			return 1
		}
	case StateStopped:
		if outline || inSlash(x, y) {
			return 1
		}
	case StateNotInstalled:
		if outline {
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
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var sum float64
			for sy := 0; sy < supersample; sy++ {
				for sx := 0; sx < supersample; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/supersample) / n
					y := (float64(py) + (float64(sy)+0.5)/supersample) / n
					sum += covered(state, x, y)
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
