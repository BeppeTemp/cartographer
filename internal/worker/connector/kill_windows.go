//go:build windows

package connector

import (
	"os/exec"
	"strconv"
)

// KillGroupOnCancel makes a cancelled command take its children with it. It is
// a copy of killGroupOnCancel in internal/gitx/proc_windows.go: a fix goes to
// both. taskkill /T walks the tree while its root is still alive; if it cannot
// run, the root alone is killed.
func KillGroupOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		pid := strconv.Itoa(cmd.Process.Pid)
		if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}

// KillGroup ends the process tree of a started command (see the Unix file).
func KillGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
}
