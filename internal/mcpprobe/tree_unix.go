//go:build !windows

package mcpprobe

import (
	"os/exec"
	"syscall"
)

// ownTree puts cmd in a process group of its own and returns the function that
// kills the whole group. Without it a timeout kills the direct child only, and
// a grandchild (npx starts node) keeps running with the secrets in its
// environment. The returned function is also cmd.Cancel and runs again when Run
// returns; killing a group that is gone is harmless.
func ownTree(cmd *exec.Cmd) (kill func()) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill = func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	cmd.Cancel = func() error { kill(); return nil }
	return kill
}
