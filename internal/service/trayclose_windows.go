//go:build windows

package service

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/BeppeTemp/cartographer/internal/defaults"
)

const wmClose = 0x0010

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW = user32.NewProc("FindWindowW")
	procPostMessage = user32.NewProc("PostMessageW")
)

// requestTrayClose asks the running tray to close its window (WM_CLOSE), which
// runs its own teardown, NIM_DELETE included. Stop-ScheduledTask ends the
// process without it and the shell keeps a dead icon until the mouse passes
// over it. FindWindowW sees only this desktop, so a tray of another user or
// session is never reached; an error means there was no window to ask.
func requestTrayClose() error {
	cls, err := windows.UTF16PtrFromString(defaults.WindowsTrayWindowClass)
	if err != nil {
		return err
	}
	hwnd, _, _ := syscall.SyscallN(procFindWindowW.Addr(), uintptr(unsafe.Pointer(cls)), 0)
	if hwnd == 0 {
		return fmt.Errorf("service: no tray window of class %s on this desktop", defaults.WindowsTrayWindowClass)
	}
	if ok, _, e := syscall.SyscallN(procPostMessage.Addr(), hwnd, wmClose, 0, 0); ok == 0 {
		return fmt.Errorf("service: post WM_CLOSE to the tray window: %w", e)
	}
	return nil
}
