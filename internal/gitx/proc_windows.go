//go:build windows

package gitx

import "os/exec"

// killGroupOnCancel is the default kill on Windows, which has no process
// groups to signal; WaitDelay still releases the caller.
func killGroupOnCancel(cmd *exec.Cmd) {}
