//go:build !windows

package recipe

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// assertGone: the grandchild the script left behind died with the process group.
func assertGone(t *testing.T, pid int) {
	t.Helper()
	if pid <= 0 {
		t.Fatal("the script wrote no grandchild pid")
	}
	for i := 0; i < 50; i++ {
		if gone(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Errorf("grandchild %d survived the timeout", pid)
}

// gone reports whether pid is dead. A killed orphan whose parent does not reap
// it (PID 1 is a plain sh in a build container) stays a zombie and kill(pid, 0)
// still succeeds, so on Linux a process in state Z counts as dead.
func gone(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false // no /proc (macOS): the kill check above is all there is
	}
	// The state is the field after the ")" that closes the command name.
	if i := strings.LastIndexByte(string(stat), ')'); i >= 0 && i+2 < len(stat) {
		return stat[i+2] == 'Z'
	}
	return false
}
