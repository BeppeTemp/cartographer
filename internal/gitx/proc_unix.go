//go:build !windows

package gitx

import (
	"os/exec"
	"syscall"
)

// killGroupOnCancel makes a cancelled command take its children with it.
// git fetch over http runs git-remote-http as a child: killing only git
// leaves that child stuck on the silent socket, one orphan per timeout.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
