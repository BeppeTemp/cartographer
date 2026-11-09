//go:build !windows

package connector

import (
	"os/exec"
	"syscall"
)

// KillGroupOnCancel makes a cancelled command take its children with it.
// It is a copy of killGroupOnCancel in internal/gitx/proc_unix.go (that one is
// private to git and unchanged by this package): a fix goes to both.
func KillGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

// KillGroup kills the process group of a started command, for the grandchildren
// a connector left behind after its own process exited (cancelling the command's
// context does nothing once Wait has returned).
func KillGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
