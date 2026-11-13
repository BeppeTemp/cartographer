//go:build !windows

package mcpprobe

import "syscall"

func processAlive(pid int) bool {
	// A killed grandchild is reparented and reaped by init; until then it can
	// linger as a zombie, which Kill(0) still answers for, so wait for ESRCH.
	return syscall.Kill(pid, 0) == nil
}
