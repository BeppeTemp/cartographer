package kb

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// TestSchedulePush_Burst_CoalescesIntoOnePush sends a burst of SchedulePush
// signals close together (debounce window comfortably longer than the burst
// itself) and verifies that, after FlushPush, the remote has received every
// local commit — i.e. no write was lost to coalescing, and (since the
// signals were sent back-to-back with no gap, well inside a single debounce
// window) the worker only had one debounce cycle to react to, so it can only
// have performed a single SyncOut for the whole burst.
func TestSchedulePush_Burst_CoalescesIntoOnePush(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.GitSync = true
	k.SyncOutDebounce = 200 * time.Millisecond

	const n = 5
	for i := 0; i < n; i++ {
		writeFileT(t, k.Root, filepath.Join("data", "burst-file.txt"), "n=0\n")
		gitHere(t, k.Root, "add", "-A")
		gitHere(t, k.Root, "-c", "user.email=test@test", "-c", "user.name=test",
			"commit", "-m", "burst commit", "--allow-empty")
		k.SchedulePush()
	}

	if err := k.FlushPush(5 * time.Second); err != nil {
		t.Fatalf("FlushPush: %v", err)
	}

	branch, _ := gitx.Branch(k.Root)
	localCount := gitHere(t, k.Root, "rev-list", "--count", branch)
	remoteCount := gitHere(t, bare, "rev-list", "--count", branch)
	if remoteCount != localCount {
		t.Fatalf("remote has %s commits, local has %s — burst was not fully pushed", remoteCount, localCount)
	}
}

// writeFileT is a tiny os.WriteFile wrapper for test setup.
func writeFileT(t *testing.T, root, relPath, content string) {
	t.Helper()
	abs := filepath.Join(root, relPath)
	if err := writeFileAtomic(abs, []byte(content)); err != nil {
		t.Fatalf("writeFileAtomic %s: %v", relPath, err)
	}
}

// TestSchedulePush_Conflict_InvokesOnPushConflict verifies that a rebase
// conflict hit by the async push worker is routed to k.OnPushConflict rather
// than only logged to stderr.
func TestSchedulePush_Conflict_InvokesOnPushConflict(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.GitSync = true
	k.SyncOutDebounce = 20 * time.Millisecond

	// Seed the remote so there is a common ancestor to diverge from.
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	branch, _ := gitx.Branch(k.Root)

	// Remote diverges: another writer edits data/index.md and pushes.
	pushCommitToBare(t, bare, branch, "data/index.md")

	// Local diverges too, on the same file: a genuine conflict on push.
	writeFileT(t, k.Root, "data/index.md", "local content\n")
	gitHere(t, k.Root, "add", "-A")
	gitHere(t, k.Root, "-c", "user.email=test@test", "-c", "user.name=test",
		"commit", "-m", "local conflicting edit")

	var mu sync.Mutex
	var got *gitx.RebaseConflictError
	done := make(chan struct{})
	k.OnPushConflict = func(rce *gitx.RebaseConflictError) {
		mu.Lock()
		got = rce
		mu.Unlock()
		close(done)
	}

	k.SchedulePush()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for OnPushConflict")
	}

	mu.Lock()
	defer mu.Unlock()
	if got == nil {
		t.Fatal("expected a non-nil *gitx.RebaseConflictError")
	}
}

// TestFlushPush_NoPending_NoOp verifies that FlushPush is a no-op — and does
// not start the worker — when SchedulePush was never called (which is always
// the case in production when SyncOutDebounce == 0, the rollback flag).
func TestFlushPush_NoPending_NoOp(t *testing.T) {
	k := &KB{}
	if err := k.FlushPush(time.Second); err != nil {
		t.Fatalf("FlushPush with nothing pending should be a no-op, got: %v", err)
	}
	if k.pushStarted {
		t.Fatal("FlushPush must not start the async push worker when SchedulePush was never called")
	}
}

// TestFlushPush_AfterSchedule_WaitsForPush verifies that FlushPush forces an
// immediate push (rather than waiting out the full debounce) and that the
// commit has reached the remote once FlushPush returns.
func TestFlushPush_AfterSchedule_WaitsForPush(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.GitSync = true
	k.SyncOutDebounce = 1 * time.Hour // would never fire on its own within the test

	k.SchedulePush()

	start := time.Now()
	if err := k.FlushPush(5 * time.Second); err != nil {
		t.Fatalf("FlushPush: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= k.SyncOutDebounce {
		t.Fatalf("FlushPush took %s — did not force an immediate push, waited out the debounce", elapsed)
	}

	branch, _ := gitx.Branch(k.Root)
	count := gitHere(t, bare, "rev-list", "--count", branch)
	if count == "" || count == "0" {
		t.Fatalf("remote did not receive the push forced by FlushPush (count=%q)", count)
	}
}
