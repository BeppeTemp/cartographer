//go:build windows

package tray

import (
	"errors"
	"image/color"
	"log"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The notification-area UI, pure Go through user32/shell32 (no cgo).
//
// Every call into the toolkit (window, menu, Shell_NotifyIconW) happens on the
// one OS thread Run locks, the thread that created the window. The poll
// goroutine reaches it only through a mutex-guarded slot plus PostMessageW;
// it never calls a UI function itself.
//
// All procs go through syscall.SyscallN, not LazyProc.Call:
// TestNoUnqualifiedProductionToolCalls reads any `.Call(` in cmd/cartographer
// as an MCP call, and the same convention keeps this package out of it.

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmTimer         = 0x0113
	wmEndSession    = 0x0016
	wmSettingChange = 0x001A
	wmNull          = 0x0000
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmApp           = 0x8000
	wmUpdate        = wmApp + 1 // a snapshot is waiting in pending
	wmTrayIcon      = wmApp + 2 // the icon's callback message

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4

	mfString    = 0x0
	mfGrayed    = 0x1
	mfDisabled  = 0x2
	mfSeparator = 0x800

	tpmRightButton = 0x2
	tpmReturnCmd   = 0x100

	smCxSmIcon = 49

	iconUID      = 1 // fixed ID, never NIF_GUID: a GUID binds to the binary's path and breaks after an in-place update (install.ps1)
	retryTimer   = 1
	retryMillis  = 1000
	retryMax     = 30 // a logon task can run before the shell has made the notification area
	tipMaxUnits  = 127
	errClassDup  = 1410 // ERROR_CLASS_ALREADY_EXISTS
	mutexName    = `Local\CartographerTray`
	className    = "CartographerTrayWindow"
	personalize  = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	lightThemeKV = "SystemUsesLightTheme"
)

var (
	user32  = windows.NewLazySystemDLL("user32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")

	procRegisterClassEx   = user32.NewProc("RegisterClassExW")
	procCreateWindowEx    = user32.NewProc("CreateWindowExW")
	procDefWindowProc     = user32.NewProc("DefWindowProcW")
	procDestroyWindow     = user32.NewProc("DestroyWindow")
	procGetMessage        = user32.NewProc("GetMessageW")
	procTranslateMessage  = user32.NewProc("TranslateMessage")
	procDispatchMessage   = user32.NewProc("DispatchMessageW")
	procPostMessage       = user32.NewProc("PostMessageW")
	procPostQuitMessage   = user32.NewProc("PostQuitMessage")
	procRegisterWindowMsg = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu   = user32.NewProc("CreatePopupMenu")
	procAppendMenu        = user32.NewProc("AppendMenuW")
	procTrackPopupMenu    = user32.NewProc("TrackPopupMenu")
	procDestroyMenu       = user32.NewProc("DestroyMenu")
	procSetForegroundWin  = user32.NewProc("SetForegroundWindow")
	procGetCursorPos      = user32.NewProc("GetCursorPos")
	procCreateIcon        = user32.NewProc("CreateIcon")
	procDestroyIcon       = user32.NewProc("DestroyIcon")
	procGetSystemMetrics  = user32.NewProc("GetSystemMetrics")
	procSetTimer          = user32.NewProc("SetTimer")
	procKillTimer         = user32.NewProc("KillTimer")
	procShellNotifyIcon   = shell32.NewProc("Shell_NotifyIconW")

	wndProcCallback = syscall.NewCallback(wndProc)

	// current is the one window of this process; the window procedure is a
	// bare callback with no receiver.
	current *winUI
)

var procGetModuleHandle = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")

// moduleHandle is this executable's HINSTANCE.
func moduleHandle() uintptr { return call(procGetModuleHandle, 0) }

func call(p *windows.LazyProc, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(p.Addr(), args...)
	return r
}

// notifyIconData is NOTIFYICONDATAW (Unicode, Vista+ layout, 976 bytes on
// 64-bit). A wrong layout silently shows no icon: TestNotifyIconDataLayout
// pins it.
type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     uintptr
}

type wndClassEx struct {
	cbSize     uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type point struct{ x, y int32 }

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

// NewUI returns the notification-area UI.
func NewUI() (UI, error) { return &winUI{}, nil }

type winUI struct {
	menu     Menu
	onAction func(Action)

	hwnd           uintptr
	taskbarCreated uint32
	hicon          uintptr
	snap           Snapshot // UI thread only
	light          bool     // the taskbar is light: draw a black glyph
	added          bool
	retries        int
	iconWarned     bool

	mu      sync.Mutex
	pending *Snapshot // written by the feeder goroutine, read on the UI thread
	done    chan struct{}
}

// Run implements UI. A second tray in the same session exits quietly: the
// task's IgnoreNew policy does not cover a hand-typed command.
func (u *winUI) Run(menu Menu, updates <-chan Snapshot, onAction func(Action)) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return err
	}
	h, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil
	}
	if h == 0 {
		return err
	}
	defer windows.CloseHandle(h)

	u.menu, u.onAction = menu, onAction
	if err := u.init(); err != nil {
		return err
	}
	go u.feed(updates, func(m uintptr) { call(procPostMessage, u.hwnd, m, 0, 0) })
	u.loop()
	return nil
}

// feed hands snapshots to the UI thread through post (PostMessageW on the
// hidden window). A closed updates channel is the caller's request to stop
// (cmdTray closes it on SIGINT/SIGTERM): it posts WM_CLOSE, whose handler
// destroys the window, so WM_DESTROY deletes the icon and quits the loop.
func (u *winUI) feed(updates <-chan Snapshot, post func(msg uintptr)) {
	for {
		select {
		case s, ok := <-updates:
			if !ok {
				post(wmClose)
				return
			}
			u.mu.Lock()
			u.pending = &s
			u.mu.Unlock()
			post(wmUpdate)
		case <-u.done:
			return
		}
	}
}

// init creates the hidden window and the icon. The window is a plain hidden
// top-level one, not HWND_MESSAGE: a message-only window never receives the
// TaskbarCreated broadcast, which is how an Explorer restart is noticed.
func (u *winUI) init() error {
	current = u
	u.done = make(chan struct{})
	u.snap = Snapshot{KBCount: -1}
	u.light = taskbarIsLight()

	inst := moduleHandle()
	cn, _ := windows.UTF16PtrFromString(className)
	wc := wndClassEx{wndProc: wndProcCallback, instance: inst, className: cn}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if atom, _, e := syscall.SyscallN(procRegisterClassEx.Addr(), uintptr(unsafe.Pointer(&wc))); atom == 0 && e != errClassDup {
		return errors.New("tray: cannot register the window class")
	}
	u.hwnd = call(procCreateWindowEx, 0, uintptr(unsafe.Pointer(cn)), uintptr(unsafe.Pointer(cn)), 0, 0, 0, 0, 0, 0, 0, inst, 0)
	if u.hwnd == 0 {
		return errors.New("tray: cannot create the window")
	}
	tc, _ := windows.UTF16PtrFromString("TaskbarCreated")
	u.taskbarCreated = uint32(call(procRegisterWindowMsg, uintptr(unsafe.Pointer(tc))))

	u.hicon = u.makeIcon()
	u.tryAdd()
	return nil
}

func (u *winUI) loop() {
	var m msg
	for {
		if int32(call(procGetMessage, uintptr(unsafe.Pointer(&m)), 0, 0, 0)) <= 0 {
			break
		}
		call(procTranslateMessage, uintptr(unsafe.Pointer(&m)))
		call(procDispatchMessage, uintptr(unsafe.Pointer(&m)))
	}
	close(u.done)
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	u := current
	if u == nil || hwnd != u.hwnd {
		return call(procDefWindowProc, hwnd, message, wParam, lParam)
	}
	switch uint32(message) {
	case wmUpdate:
		u.mu.Lock()
		p := u.pending
		u.pending = nil
		u.mu.Unlock()
		if p != nil {
			u.snap = *p
			u.refresh()
		}
		return 0
	case wmTrayIcon:
		switch uint32(lParam & 0xFFFF) {
		case wmLButtonUp, wmRButtonUp:
			u.showMenu()
		case wmLButtonDblClk:
			u.onAction(ActionOpenAtlas)
		}
		return 0
	case wmTimer:
		// Startup retry: the shell may not have created the notification area yet.
		u.retries++
		if u.notify(nimAdd) {
			u.added = true
		}
		if u.added || u.retries >= retryMax {
			call(procKillTimer, u.hwnd, retryTimer)
		}
		return 0
	case wmSettingChange:
		// The taskbar theme flipped (ImmersiveColorSet): redraw the glyph.
		if l := taskbarIsLight(); l != u.light {
			u.light = l
			u.refresh()
		}
		return 0
	case wmEndSession:
		if wParam != 0 {
			u.notify(nimDelete)
		}
		return 0
	case wmClose:
		call(procDestroyWindow, u.hwnd)
		return 0
	case wmDestroy:
		u.notify(nimDelete)
		if u.hicon != 0 {
			call(procDestroyIcon, u.hicon)
			u.hicon = 0
		}
		call(procPostQuitMessage, 0)
		return 0
	}
	if u.taskbarCreated != 0 && uint32(message) == u.taskbarCreated {
		// Explorer restarted: its notification area is empty again.
		u.tryAdd()
		return 0
	}
	return call(procDefWindowProc, hwnd, message, wParam, lParam)
}

// refresh redraws the icon and tooltip from u.snap.
func (u *winUI) refresh() {
	old := u.hicon
	if h := u.makeIcon(); h != 0 {
		u.hicon = h
	} else if !u.iconWarned {
		// Keep the working icon rather than modify the entry to none.
		u.iconWarned = true
		log.Printf("tray: CreateIcon failed, keeping the previous icon")
	}
	if !u.notify(nimModify) {
		u.tryAdd()
	}
	if old != 0 && old != u.hicon {
		call(procDestroyIcon, old)
	}
}

// tryAdd adds the icon and, when the shell refuses (not ready at logon, just
// restarted), re-arms the bounded retry timer.
func (u *winUI) tryAdd() {
	if u.added = u.notify(nimAdd); u.added {
		return
	}
	u.retries = 0
	call(procSetTimer, u.hwnd, retryTimer, retryMillis, 0)
}

func (u *winUI) notify(op uintptr) bool {
	d := notifyIconData{hWnd: u.hwnd, uID: iconUID, uFlags: nifMessage | nifIcon | nifTip, uCallbackMessage: wmTrayIcon, hIcon: u.hicon}
	d.cbSize = uint32(unsafe.Sizeof(d))
	tip, _ := windows.UTF16FromString(truncateUTF16(StatusLine(u.snap), tipMaxUnits))
	copy(d.szTip[:], tip)
	return call(procShellNotifyIcon, op, uintptr(unsafe.Pointer(&d))) != 0
}

// makeIcon builds the HICON of the current state at the small-icon size, white
// on a dark taskbar and black on a light one (Windows has no template icons).
func (u *winUI) makeIcon() uintptr {
	size := int(call(procGetSystemMetrics, smCxSmIcon))
	if size <= 0 {
		size = 16
	}
	fg := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	if u.light {
		fg = color.NRGBA{A: 255}
	}
	img := Render(u.snap.State, size, fg)
	xor := bgra(img.Pix)
	and := make([]byte, size*((size+15)/16*2)) // 1-bpp mask, all opaque: alpha decides
	return call(procCreateIcon, moduleHandle(), uintptr(size), uintptr(size), 1, 32, uintptr(unsafe.Pointer(&and[0])), uintptr(unsafe.Pointer(&xor[0])))
}

func (u *winUI) showMenu() {
	h := call(procCreatePopupMenu)
	if h == 0 {
		return
	}
	defer call(procDestroyMenu, h)
	for _, it := range MenuItems(u.menu, u.snap) {
		switch {
		case it.Separator:
			call(procAppendMenu, h, mfSeparator, 0, 0)
		default:
			// The status line has command id 0: it would collide with Action 0.
			flags, id := uintptr(mfString), uintptr(it.CommandID())
			if !it.Enabled {
				flags |= mfGrayed | mfDisabled
			}
			t, _ := windows.UTF16PtrFromString(it.Text)
			call(procAppendMenu, h, flags, id, uintptr(unsafe.Pointer(t)))
		}
	}
	var p point
	call(procGetCursorPos, uintptr(unsafe.Pointer(&p)))
	// Documented dismiss-on-click-away fix: foreground before, WM_NULL after.
	call(procSetForegroundWin, u.hwnd)
	cmd := call(procTrackPopupMenu, h, tpmReturnCmd|tpmRightButton, uintptr(p.x), uintptr(p.y), 0, u.hwnd, 0)
	call(procPostMessage, u.hwnd, wmNull, 0, 0)
	if cmd < menuCommandBase {
		return
	}
	a := Action(cmd - menuCommandBase)
	u.onAction(a)
	if a == ActionQuit {
		call(procDestroyWindow, u.hwnd)
	}
}

// taskbarIsLight reads the taskbar theme; absent or unreadable means dark.
func taskbarIsLight() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, personalize, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(lightThemeKV)
	return err == nil && v != 0
}

// bgra converts straight-alpha RGBA pixels to the BGRA order CreateIcon wants.
func bgra(rgba []byte) []byte {
	out := make([]byte, len(rgba))
	for i := 0; i+3 < len(rgba); i += 4 {
		out[i], out[i+1], out[i+2], out[i+3] = rgba[i+2], rgba[i+1], rgba[i], rgba[i+3]
	}
	return out
}
