//go:build darwin

package tray

import (
	"errors"
	"image/color"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// The menu-bar UI, NSStatusItem driven through purego (no cgo, D1493). This is
// the only file that imports purego, so Windows and Linux binaries never link it.
//
// Cocoa wants the process's first thread: init locks the main goroutine to it
// and cmdTray calls UI.Run from main before anything else blocks. Every AppKit
// call happens on that thread. The poll goroutine reaches it only through a
// mutex-guarded slot plus performSelectorOnMainThread:, never by calling AppKit.

// init, not Run: only init guarantees the main goroutine stays on the process's
// first thread. It pins every darwin command's main goroutine, which costs the
// others nothing.
func init() { runtime.LockOSThread() }

const (
	appKitPath         = "/System/Library/Frameworks/AppKit.framework/AppKit"
	activationPolicy   = 1  // NSApplicationActivationPolicyAccessory: no Dock icon
	variableLength     = -1 // NSVariableStatusItemLength
	iconPoints         = 18
	iconPixels         = 2 * iconPoints // rendered @2x
	eventApplicationEv = 15             // NSEventTypeApplicationDefined
)

// selectorActions maps each target selector to its menu action and key
// equivalent. No images: macOS 27 hides menu symbols by default (D1495).
var selectorActions = []struct {
	sel    string
	action Action
	key    string
}{
	{"openAtlas:", ActionOpenAtlas, "o"},
	{"restartService:", ActionRestart, "r"},
	{"openLog:", ActionOpenLog, "l"},
	{"quitTray:", ActionQuit, "q"},
}

// Layout of an information row's view, in points. rowInset lines its text up
// with the action titles.
const (
	rowInset    = 14
	rowPadV     = 3
	detailSize  = 11
	trailingGap = 24 // between a row's text and its trailing status

	autoresizeMinX  = 1 // NSViewMinXMargin
	autoresizeWidth = 2 // NSViewWidthSizable
)

var (
	pine = color.NRGBA{R: 0x36, G: 0x5D, B: 0x50, A: 0xFF}
	sage = color.NRGBA{R: 0x91, G: 0xB9, B: 0xA0, A: 0xFF}
)

const (
	selOpenKB = "openKB:"      // a KB row: the action is the sender's tag
	selApply  = "applyUpdate:" // a snapshot is waiting in pending
	selStop   = "stopLoop:"    // updates was closed: leave the run loop
)

type nsSize struct{ W, H float64 }
type nsPoint struct{ X, Y float64 }

// NewUI returns the menu-bar UI.
func NewUI() (UI, error) { return &macUI{}, nil }

type macUI struct {
	menu     Menu
	onAction func(Action)

	app, item, button, target objc.ID
	statusMenu                objc.ID
	snap                      Snapshot // the newest applied; read on the main thread only
	images                    map[State]objc.ID

	mu      sync.Mutex
	pending *Snapshot
}

func s(name string) objc.SEL { return objc.RegisterName(name) }
func class(name string) objc.ID {
	return objc.ID(objc.GetClass(name))
}

func nsString(v string) objc.ID {
	return class("NSString").Send(s("stringWithUTF8String:"), v)
}

// pool runs fn inside an autorelease pool: setup and every update leak without
// one, and a pool drained out of order crashes.
func pool(fn func()) {
	p := class("NSAutoreleasePool").Send(s("alloc")).Send(s("init"))
	defer p.Send(s("drain"))
	fn()
}

// Run implements UI. The single-instance guard is SingleInstance, taken by
// cmdTray before the first poll.
func (u *macUI) Run(menu Menu, updates <-chan Snapshot, onAction func(Action)) error {
	u.menu, u.onAction = menu, onAction
	// AppKit (which pulls in Foundation) must be loaded before the first pool:
	// NSAutoreleasePool does not exist until then.
	if _, err := purego.Dlopen(appKitPath, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		return err
	}
	var err error
	pool(func() { err = u.setup() })
	if err != nil {
		return err
	}
	go u.feed(updates)
	u.app.Send(s("run"))
	pool(func() {
		class("NSStatusBar").Send(s("systemStatusBar")).Send(s("removeStatusItem:"), u.item)
	})
	return nil
}

func (u *macUI) setup() error {
	u.app = class("NSApplication").Send(s("sharedApplication"))
	u.app.Send(s("setActivationPolicy:"), activationPolicy)
	if err := u.registerTarget(); err != nil {
		return err
	}
	u.item = class("NSStatusBar").Send(s("systemStatusBar")).Send(s("statusItemWithLength:"), float64(variableLength))
	if u.item != 0 {
		u.item.Send(s("retain"))
		u.button = u.item.Send(s("button"))
	}
	if u.item == 0 || u.button == 0 {
		return errors.New("tray: the menu bar gave no status item (run it from a desktop login)")
	}
	u.images = map[State]objc.ID{}
	for _, st := range []State{StateRunning, StateStarting, StateStopped, StateNotInstalled} {
		u.images[st] = nsImage(PNG(st, iconPixels), iconPoints)
	}
	u.statusMenu = newMenu()
	u.statusMenu.Send(s("setDelegate:"), u.target)
	u.item.Send(s("setMenu:"), u.statusMenu)
	u.apply(Snapshot{KBCount: -1})
	return nil
}

// registerTarget creates the delegate class (once per process) whose selectors
// the menu items send, and one instance of it.
func (u *macUI) registerTarget() error {
	var methods []objc.MethodDef
	for _, sa := range selectorActions {
		a := sa.action
		methods = append(methods, objc.MethodDef{Cmd: s(sa.sel), Fn: func(objc.ID, objc.SEL, objc.ID) {
			u.onAction(a)
			if a == ActionQuit {
				u.stop() // Quit leaves the tray only; the server keeps running
			}
		}})
	}
	methods = append(methods,
		objc.MethodDef{Cmd: s("menuNeedsUpdate:"), Fn: func(_ objc.ID, _ objc.SEL, menu objc.ID) {
			if menu == u.statusMenu {
				menu.Send(s("removeAllItems"))
				u.fill(menu, MenuItems(u.menu, u.snap))
			}
		}},
		objc.MethodDef{Cmd: s(selOpenKB), Fn: func(_ objc.ID, _ objc.SEL, sender objc.ID) {
			u.onAction(Action(objc.Send[int](sender, s("tag"))))
		}},
		objc.MethodDef{Cmd: s(selApply), Fn: func(objc.ID, objc.SEL, objc.ID) { u.applyPending() }},
		objc.MethodDef{Cmd: s(selStop), Fn: func(objc.ID, objc.SEL, objc.ID) { u.stop() }},
	)
	cls, err := objc.RegisterClass("CartographerTrayTarget", objc.GetClass("NSObject"), nil, nil, methods)
	if err != nil {
		return err
	}
	u.target = objc.ID(cls).Send(s("new"))
	return nil
}

// feed hands snapshots to the main thread. A closed updates channel is the
// caller's request to stop (cmdTray closes it on SIGINT/SIGTERM).
func (u *macUI) feed(updates <-chan Snapshot) {
	runtime.LockOSThread() // onMain's pool must drain on the thread that made it
	for sn := range updates {
		u.mu.Lock()
		u.pending = &sn
		u.mu.Unlock()
		u.onMain(selApply)
	}
	u.onMain(selStop)
}

func (u *macUI) onMain(sel string) {
	// Off the main thread: no run loop pool is active here, so bring our own.
	pool(func() {
		u.target.Send(s("performSelectorOnMainThread:withObject:waitUntilDone:"), s(sel), objc.ID(0), false)
	})
}

func (u *macUI) applyPending() {
	u.mu.Lock()
	p := u.pending
	u.pending = nil
	u.mu.Unlock()
	if p != nil {
		pool(func() { u.apply(*p) })
	}
}

// stop leaves [NSApp run]. stop: only takes effect after an event, so an
// application-defined one is posted (terminate: would exit(0) past Go's cleanup).
func (u *macUI) stop() {
	u.app.Send(s("stop:"), objc.ID(0))
	ev := class("NSEvent").Send(s("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:"),
		eventApplicationEv, nsPoint{}, 0, float64(0), 0, objc.ID(0), 0, 0, 0)
	u.app.Send(s("postEvent:atStart:"), ev, true)
}

// apply redraws the icon, tooltip and menu for a snapshot (main thread only).
func (u *macUI) apply(sn Snapshot) {
	u.button.Send(s("setImage:"), u.images[sn.State])
	u.button.Send(s("setToolTip:"), nsString(StatusLine(sn)))

	u.snap = sn // the menu is filled from it when it opens (menuNeedsUpdate:)
}

// newMenu returns an empty NSMenu (retained: the owner releases it).
func newMenu() objc.ID {
	m := class("NSMenu").Send(s("alloc")).Send(s("initWithTitle:"), nsString("Cartographer"))
	m.Send(s("setAutoenablesItems:"), false) // we say which rows are enabled
	return m
}

// fill adds rows to m. The status menu is filled each time it opens, so the
// accent follows a light/dark switch and the rows the newest snapshot.
func (u *macUI) fill(m objc.ID, items []MenuItem) {
	for _, it := range items {
		var mi objc.ID
		switch {
		case it.Separator:
			m.Send(s("addItem:"), class("NSMenuItem").Send(s("separatorItem")))
			continue
		case it.Status:
			mi = u.infoItem(it)
		default:
			sel, key := selOpenKB, ""
			for _, sa := range selectorActions {
				if sa.action == it.Action {
					sel, key = sa.sel, sa.key
				}
			}
			if it.Children != nil {
				sel, key = "", "" // a submenu parent acts on nothing
			}
			mi = class("NSMenuItem").Send(s("alloc")).Send(s("initWithTitle:action:keyEquivalent:"), nsString(it.Text), s0(sel), nsString(key))
			if sel != "" {
				mi.Send(s("setTarget:"), u.target)
				mi.Send(s("setTag:"), int(it.Action))
			}
			if it.Children != nil {
				sub := newMenu()
				u.fill(sub, it.Children)
				mi.Send(s("setSubmenu:"), sub)
				sub.Send(s("release"))
			}
			mi.Send(s("setEnabled:"), it.Enabled)
		}
		m.Send(s("addItem:"), mi)
		mi.Send(s("release"))
	}
}

// infoItem is an information row as a menu item with a view, which is neither
// greyed (a disabled item) nor highlighted on hover (an enabled one) (D1495).
func (u *macUI) infoItem(it MenuItem) objc.ID {
	menuFont := class("NSFont").Send(s("menuFontOfSize:"), 0.0)
	size := objc.Send[float64](menuFont, s("pointSize"))
	text := nsColor("labelColor")
	var lines []objc.ID // top to bottom
	var trailing objc.ID
	switch {
	case it.Header:
		lines = append(lines, label(it.Text, class("NSFont").Send(s("boldSystemFontOfSize:"), size), text))
		if it.Detail != "" {
			lines = append(lines, label(it.Detail, class("NSFont").Send(s("systemFontOfSize:"), float64(detailSize)), text))
		}
	case it.Section:
		lines = append(lines, label(strings.ToUpper(it.Text), class("NSFont").Send(s("boldSystemFontOfSize:"), float64(detailSize)), u.accent()))
	default:
		lines = append(lines, label(it.Text, menuFont, text))
	}
	if it.Badge != "" {
		trailing = label(it.Badge, menuFont, u.toneText(it.Tone))
	}

	x := float64(rowInset)
	view := class("NSView").Send(s("alloc")).Send(s("init"))
	var width, height float64
	sizes := make([]nsSize, len(lines))
	for i, l := range lines {
		sizes[i] = objc.Send[nsSize](l, s("fittingSize"))
		width = max(width, sizes[i].W)
		height += sizes[i].H
	}
	var tsize nsSize
	if trailing != 0 {
		tsize = objc.Send[nsSize](trailing, s("fittingSize"))
		width += trailingGap + tsize.W
	}
	total := x + width + rowInset
	view.Send(s("setFrameSize:"), nsSize{total, height + 2*rowPadV})
	// The menu widens a width-sizable view to its own width; the trailing
	// status keeps to the right edge, like a key equivalent.
	view.Send(s("setAutoresizingMask:"), autoresizeWidth)
	y := float64(rowPadV) // AppKit's y grows upwards: lay out from the bottom line
	for i := len(lines) - 1; i >= 0; i-- {
		lines[i].Send(s("setFrameOrigin:"), nsPoint{x, y})
		view.Send(s("addSubview:"), lines[i])
		y += sizes[i].H
	}
	if trailing != 0 { // on the first line's baseline row
		trailing.Send(s("setFrameOrigin:"), nsPoint{total - rowInset - tsize.W, y - sizes[0].H + (sizes[0].H-tsize.H)/2})
		trailing.Send(s("setAutoresizingMask:"), autoresizeMinX)
		view.Send(s("addSubview:"), trailing)
	}
	mi := class("NSMenuItem").Send(s("alloc")).Send(s("init"))
	mi.Send(s("setView:"), view)
	view.Send(s("release"))
	return mi
}

// dark reports whether menus are drawn dark right now.
func (u *macUI) dark() bool {
	name := u.app.Send(s("effectiveAppearance")).Send(s("name"))
	return name != 0 && objc.Send[bool](name, s("containsString:"), nsString("Dark"))
}

// accent is the brand accent (docs/brand/index.md): sage on dark, pine on light.
func (u *macUI) accent() objc.ID {
	c := pine
	if u.dark() {
		c = sage
	}
	return class("NSColor").Send(s("colorWithSRGBRed:green:blue:alpha:"),
		float64(c.R)/255, float64(c.G)/255, float64(c.B)/255, 1.0)
}

// toneText is the colour of a row's trailing status.
func (u *macUI) toneText(t Tone) objc.ID {
	switch t {
	case ToneOK:
		return u.accent()
	case ToneBusy:
		return nsColor("systemOrangeColor")
	case ToneDown:
		return nsColor("systemRedColor")
	case ToneUpdate:
		return nsColor("systemBlueColor")
	}
	return nsColor("labelColor")
}

func nsColor(name string) objc.ID { return class("NSColor").Send(s(name)) }

// label is a non-editable text field. The attribute keys are the values of
// NSFontAttributeName and NSForegroundColorAttributeName.
func label(text string, font, color objc.ID) objc.ID {
	attrs := class("NSMutableDictionary").Send(s("dictionary"))
	attrs.Send(s("setObject:forKey:"), font, nsString("NSFont"))
	attrs.Send(s("setObject:forKey:"), color, nsString("NSColor"))
	str := class("NSAttributedString").Send(s("alloc")).Send(s("initWithString:attributes:"), nsString(text), attrs)
	str.Send(s("autorelease"))
	return class("NSTextField").Send(s("labelWithAttributedString:"), str)
}

func s0(name string) objc.SEL {
	if name == "" {
		return 0
	}
	return s(name)
}

// nsImage turns PNG bytes into a template NSImage of points x points, which the
// system tints for light and dark menu bars.
func nsImage(png []byte, points float64) objc.ID {
	data := class("NSData").Send(s("dataWithBytes:length:"), unsafe.Pointer(&png[0]), len(png))
	img := class("NSImage").Send(s("alloc")).Send(s("initWithData:"), data)
	img.Send(s("setSize:"), nsSize{points, points})
	img.Send(s("setTemplate:"), true)
	return img
}
