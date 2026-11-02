// Package procattr sets the process attributes every child of Cartographer
// shares. Today that is one: on Windows a child is created without a console
// window (D1479 follow-up, D1518).
//
// A process that detached its own console (FreeConsole, D1479) gives every
// console-subsystem child -- powershell.exe, git.exe, sops.exe, taskkill.exe --
// a new, visible console unless the child is created with CREATE_NO_WINDOW. So
// every exec.Cmd reachable from `serve`, `sync` and `tray` goes through Hide;
// the guard test in this package fails on a call that does not.
package procattr

import "os/exec"

// Hide makes cmd start without a console window on Windows. It is a no-op
// elsewhere, keeps any other SysProcAttr field the caller already set, and
// returns cmd so a call can wrap a constructor: Hide(exec.Command(...)).
func Hide(cmd *exec.Cmd) *exec.Cmd {
	hide(cmd)
	return cmd
}
