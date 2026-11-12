package tray

import "image/color"

// The colour of a row's status text on the macOS menu (tray_darwin.go builds
// its NSColor from this table), kept here, without AppKit, so that a test on
// every OS can check it. The system colours it replaces (systemOrange,
// systemRed, systemBlue) are drawn for large controls and fall below 4.5:1 on
// a light menu.

// toneColors is a tone's colour in the light and in the dark appearance.
type toneColors struct{ Light, Dark color.NRGBA }

// toneTable holds every tone that has a fixed colour. ToneNone and ToneIdle
// are absent on purpose: they use the system label colour, which the system
// keeps readable in both appearances. ToneOK is the brand accent
// (docs/brand/index.md): pine on light, sage on dark.
var toneTable = map[Tone]toneColors{
	ToneOK:     {Light: rgb(0x36, 0x5D, 0x50), Dark: rgb(0x91, 0xB9, 0xA0)},
	ToneBusy:   {Light: rgb(0x8F, 0x52, 0x00), Dark: rgb(0xFF, 0x9F, 0x0A)},
	ToneDown:   {Light: rgb(0xB3, 0x26, 0x1E), Dark: rgb(0xFF, 0x6A, 0x63)},
	ToneUpdate: {Light: rgb(0x0A, 0x5C, 0xC4), Dark: rgb(0x51, 0x9C, 0xFF)},
}

// Approximations of the menu background, which is a translucent material that
// takes some of the wallpaper: a status text must reach 4.5:1 on each sample of
// its appearance. The samples are chosen, not measured, and not values the
// system publishes; the outer ones (#E0E0E0, #333333) leave a margin for a
// bright or a dark wallpaper behind the menu.
var (
	menuLight = []color.NRGBA{rgb(0xF6, 0xF6, 0xF6), rgb(0xEC, 0xEC, 0xEC), rgb(0xE0, 0xE0, 0xE0)}
	menuDark  = []color.NRGBA{rgb(0x1E, 0x1E, 0x1E), rgb(0x2A, 0x2A, 0x2A), rgb(0x33, 0x33, 0x33)}
)

func rgb(r, g, b uint8) color.NRGBA { return color.NRGBA{R: r, G: g, B: b, A: 0xFF} }

// toneColor is the fixed colour of tone t in the given appearance; false when
// the tone uses the system label colour.
func toneColor(t Tone, dark bool) (color.NRGBA, bool) {
	c, ok := toneTable[t]
	if !ok {
		return color.NRGBA{}, false
	}
	if dark {
		return c.Dark, true
	}
	return c.Light, true
}
