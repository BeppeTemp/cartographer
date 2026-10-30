//go:build !windows

package recipe

import (
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
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Errorf("grandchild %d survived the timeout", pid)
}
