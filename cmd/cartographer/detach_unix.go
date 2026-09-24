//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the child in a session of its own, so it outlives the
// hook that started it and no terminal signal reaches it. Its standard
// streams stay nil, which exec maps to the null device: nothing holds the
// hook's stdout open.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
