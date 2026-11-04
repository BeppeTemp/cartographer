package kb

import (
	"errors"
	"os"
	"path/filepath"
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
	// The timeout kills git with its whole process tree, this hook included
	// (a process group on Unix, the tree on Windows, #1163): a hook left
	// running would hold the bare repository open and fail t.TempDir cleanup.
	hook := filepath.Join(bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { gitx.PushTimeout = d }(gitx.PushTimeout)
	gitx.PushTimeout = time.Second

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
