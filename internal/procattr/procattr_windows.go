//go:build windows

package procattr

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW of CreateProcess: the console-subsystem
// child runs with no console at all instead of opening one.
const createNoWindow = 0x08000000

func hide(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
