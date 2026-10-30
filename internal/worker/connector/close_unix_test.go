//go:build !windows

package connector

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A stdio connector that forks a long-lived grandchild: Close takes the whole
// process group, so nothing is orphaned.
func TestCloseKillsTheGrandchild(t *testing.T) {
	t.Setenv("HELPER_FLAG", "1")
	t.Setenv("CONNECTOR_TEST_GRANDCHILD_PID", "")
	pidFile := t.TempDir() + "/pid"
	exe, _ := os.Executable()
	cfg := Config{Name: "wiki", Transport: TransportStdio, Command: "/bin/sh",
		Args:  []string{"-c", `sleep 300 & echo $! > "$PIDFILE"; exec "$0"`, exe},
		Env:   map[string]string{"CONNECTOR_TEST_HELPER": "HELPER_FLAG", "PIDFILE": "PIDFILE_VAR"},
		Tools: []string{"read"}, CallTimeout: 20 * time.Second, MaxResultBytes: 100}
	t.Setenv("PIDFILE_VAR", pidFile)
	ctx := context.Background()
	s := Open(ctx, []Config{cfg}, nil)
	if _, err := s.Tools(ctx, "wiki"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(raw[:len(raw)-1]))
	if gone(pid) {
		t.Fatalf("grandchild %d is not running before Close", pid)
	}
	s.Close()
	for i := 0; i < 50; i++ {
		if gone(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
	t.Fatalf("grandchild %d outlived Close", pid)
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
