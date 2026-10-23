package kb

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// A push that hits gitx.PushTimeout is terminal for that SyncOut: retrying five
// times would hold the KB's git lock for minutes (#1004). Not parallel: it sets
// a package variable.
func TestSyncOut_PushTimeoutIsNotRetried(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	if err := k.WriteFileAtomic("data/a.md", []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("write a"); err != nil {
		t.Fatal(err)
	}
	// The remote answers everything but stalls the push itself in its hook.
	// On Windows the timeout kills git but not this hook, which would keep
	// the bare repository open past the test: the hook is bounded and, if it
	// is still running when the test ends, the test waits for it before
	// t.TempDir is removed. A hook that never started, or that was killed with
	// git, holds nothing: that is not a failure of the push-timeout contract.
	hook := filepath.Join(bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch \"$GIT_DIR/hook-started\"\nsleep 3\ntouch \"$GIT_DIR/hook-done\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { gitx.PushTimeout = d }(gitx.PushTimeout)
	gitx.PushTimeout = time.Second

	t.Cleanup(func() {
		if runtime.GOOS != "windows" {
			return // the process group kill took the hook with git
		}
		exists := func(name string) bool {
			_, err := os.Stat(filepath.Join(bare, name))
			return err == nil
		}
		deadline := time.Now().Add(20 * time.Second)
		for exists("hook-started") && !exists("hook-done") {
			if time.Now().After(deadline) {
				t.Log("the stalled hook was still running at the deadline; TempDir cleanup decides")
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})

	start := time.Now()
	err := k.SyncOut()
	var rte *gitx.RemoteTimeoutError
	if !errors.As(err, &rte) {
		t.Fatalf("SyncOut = %v, want a RemoteTimeoutError", err)
	}
	if !strings.Contains(err.Error(), "git push") {
		t.Errorf("SyncOut = %v, want the push to be what timed out", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("SyncOut took %s: the timeout was retried", took)
	}
	if s := k.GitStatusSnapshot(); s.State != "failed" || s.Attempts != 1 {
		t.Errorf("status = %+v, want failed after one attempt", s)
	}
}
