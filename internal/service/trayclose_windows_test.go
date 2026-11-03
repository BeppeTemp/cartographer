//go:build windows

package service

import (
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/BeppeTemp/cartographer/internal/defaults"
)

// A window of the tray's class receives WM_CLOSE; with none, the request fails
// so the caller falls back to Stop-ScheduledTask.
func TestRequestTrayClose(t *testing.T) {
	if err := requestTrayClose(); err == nil {
		t.Fatal("no tray window exists, want an error")
	}

	got := make(chan struct{}, 1)
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		u32 := windows.NewLazySystemDLL("user32.dll")
		inst, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
		cls, _ := windows.UTF16PtrFromString(defaults.WindowsTrayWindowClass)
		proc := syscall.NewCallback(func(hwnd, msg, wp, lp uintptr) uintptr {
			if msg == wmClose {
				got <- struct{}{}
				u32.NewProc("PostQuitMessage").Call(0)
				return 0
			}
			r, _, _ := u32.NewProc("DefWindowProcW").Call(hwnd, msg, wp, lp)
			return r
		})
		wc := struct {
			size, style uint32
			wndProc     uintptr
			clsExtra    int32
			wndExtra    int32
			instance    uintptr
			icon        uintptr
			cursor      uintptr
			background  uintptr
			menu        *uint16
			className   *uint16
			iconSm      uintptr
		}{wndProc: proc, instance: inst, className: cls}
		wc.size = uint32(unsafe.Sizeof(wc))
		if a, _, e := u32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc))); a == 0 {
			ready <- e
			return
		}
		h, _, e := u32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(cls)), 0, 0, 0, 0, 0, 0, 0, inst, 0)
		if h == 0 {
			ready <- e
			return
		}
		ready <- nil
		var msg [64]byte // MSG is 48 bytes on 64-bit Windows
		for {
			r, _, _ := u32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			u32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg[0])))
		}
	}()
	if err := <-ready; err != nil {
		t.Fatalf("test window: %v", err)
	}
	if err := requestTrayClose(); err != nil {
		t.Fatalf("requestTrayClose: %v", err)
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never received WM_CLOSE")
	}
}
