//go:build windows

package gitx

import (
	"os/exec"
	"strconv"
)

// killGroupOnCancel makes a cancelled command take its children with it, the
// Windows counterpart of the Unix process-group kill. The git on PATH is often
// the Git\cmd\git.exe shim, which runs the real git as a child: killing only
// the shim leaves that git (and any hook it started) running in the
// repository, holding it open (#1163). taskkill /T walks the tree while its
// root is still alive; if it cannot run, the root alone is killed.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		pid := strconv.Itoa(cmd.Process.Pid)
		if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
