//go:build darwin

package tray

import (
	"errors"
	"runtime"
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

func init() { runtime.LockOSThread() }

const (
	appKitPath         = "/System/Library/Frameworks/AppKit.framework/AppKit"
	activationPolicy   = 1  // NSApplicationActivationPolicyAccessory: no Dock icon
	variableLength     = -1 // NSVariableStatusItemLength
	iconPoints         = 18
	iconPixels         = 2 * iconPoints // rendered @2x
	eventApplicationEv = 15             // NSEventTypeApplicationDefined
)

// selectorActions maps each target selector to the menu action it triggers.
var selectorActions = []struct {
	sel    string
	action Action
}{
	{"openAtlas:", ActionOpenAtlas},
	{"restartService:", ActionRestart},
	{"openLog:", ActionOpenLog},
	{"quitTray:", ActionQuit},
}

const (
	selApply = "applyUpdate:" // a snapshot is waiting in pending
	selStop  = "stopLoop:"    // updates was closed: leave the run loop
)

type nsSize struct{ W, H float64 }
type nsPoint struct{ X, Y float64 }

// NewUI returns the menu-bar UI.
func NewUI() (UI, error) { return &macUI{}, nil }

type macUI struct {
	menu     Menu
	onAction func(Action)

	app, item, button, target objc.ID
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

// Run implements UI.
func (u *macUI) Run(menu Menu, updates <-chan Snapshot, onAction func(Action)) error {
	u.menu, u.onAction = menu, onAction
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
	if _, err := purego.Dlopen(appKitPath, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		return err
	}
	u.app = class("NSApplication").Send(s("sharedApplication"))
	if u.app == 0 {
		return errors.New("tray: no GUI session (NSApplication is unavailable)")
	}
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
		return errors.New("tray: no GUI session (the menu bar is unavailable; run it from a desktop login)")
	}
	u.images = map[State]objc.ID{}
	for _, st := range []State{StateRunning, StateStarting, StateStopped, StateNotInstalled} {
		u.images[st] = templateImage(PNG(st, iconPixels))
	}
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
	for sn := range updates {
		u.mu.Lock()
		u.pending = &sn
		u.mu.Unlock()
		u.onMain(selApply)
	}
	u.onMain(selStop)
}

func (u *macUI) onMain(sel string) {
	u.target.Send(s("performSelectorOnMainThread:withObject:waitUntilDone:"), s(sel), objc.ID(0), false)
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

	m := class("NSMenu").Send(s("alloc")).Send(s("initWithTitle:"), nsString("Cartographer"))
	m.Send(s("setAutoenablesItems:"), false) // we say which rows are enabled
	for _, it := range MenuItems(u.menu, sn) {
		if it.Separator {
			m.Send(s("addItem:"), class("NSMenuItem").Send(s("separatorItem")))
			continue
		}
		sel := ""
		for _, sa := range selectorActions {
			if !it.Status && sa.action == it.Action {
				sel = sa.sel
			}
		}
		mi := class("NSMenuItem").Send(s("alloc")).Send(s("initWithTitle:action:keyEquivalent:"), nsString(it.Text), s0(sel), nsString(""))
		if sel != "" {
			mi.Send(s("setTarget:"), u.target)
		}
		mi.Send(s("setEnabled:"), it.Enabled && !it.Status)
		m.Send(s("addItem:"), mi)
		mi.Send(s("release"))
	}
	u.item.Send(s("setMenu:"), m)
	m.Send(s("release"))
}

func s0(name string) objc.SEL {
	if name == "" {
		return 0
	}
	return s(name)
}

// templateImage turns PNG bytes into a template NSImage, so the system tints
// it for light and dark menu bars.
func templateImage(png []byte) objc.ID {
	data := class("NSData").Send(s("dataWithBytes:length:"), unsafe.Pointer(&png[0]), len(png))
	img := class("NSImage").Send(s("alloc")).Send(s("initWithData:"), data)
	img.Send(s("setSize:"), nsSize{iconPoints, iconPoints})
	img.Send(s("setTemplate:"), true)
	return img
}
