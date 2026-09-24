//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess       = 0x00000008 // DETACHED_PROCESS: no console inherited
	createNewProcessGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP: no Ctrl+C from the parent
)

// detachProcess starts the child without the hook's console, so it outlives
// the hook that started it.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup, HideWindow: true}
}
