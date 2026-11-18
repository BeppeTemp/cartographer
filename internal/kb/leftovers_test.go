package kb

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// leftOverUnderLock leaves data/left.md on disk as a failed write whose restore
// a foreign index.lock refused for the whole wait (#2241), and returns the lock.
func leftOverUnderLock(t *testing.T, k *KB) string {
	t.Helper()
	if err := k.WriteFileAtomic("data/left.md", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	// Staged: the restore without the index refuses it, only a reset settles it.
	gitHere(t, k.Root, "add", "-A")
	lock := filepath.Join(k.Root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := k.DiscardUncommitted(); err == nil {
		t.Fatal("the discard under a held lock must fail")
	}
	if !k.LeftoversPending() {
		t.Fatal("the refused discard recorded nothing")
	}
	return lock
}

// #2254: once the lock is gone the commit discards the leftover first, so the
// file of a write answered "not saved" never reaches HEAD.
func TestCommitOpAs_DiscardsALeftoverBeforeCommitting(t *testing.T) {
	k, _ := cleanRemoteKB(t)
	lock := leftOverUnderLock(t, k)
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOpAs("external changes", "", ""); err != nil {
		t.Fatalf("CommitOpAs: %v", err)
	}
	if out := gitHere(t, k.Root, "ls-tree", "-r", "--name-only", "HEAD"); strings.Contains(out, "data/left.md") {
		t.Fatal("the file of the failed write was committed")
	}
	if _, err := os.Stat(filepath.Join(k.Root, "data", "left.md")); !os.IsNotExist(err) {
		t.Fatalf("the leftover is still on disk (%v)", err)
	}
}

// #2254: while the lock holds the leftover, no commit goes through.
func TestCommitOpAs_RefusesWhileALeftoverIsHeld(t *testing.T) {
	k, _ := cleanRemoteKB(t)
	leftOverUnderLock(t, k)
	head := gitHere(t, k.Root, "rev-parse", "HEAD")
	if _, err := k.CommitOpAs("external changes", "", ""); !errors.Is(err, ErrLeftoversPending) {
		t.Fatalf("CommitOpAs error = %v, want ErrLeftoversPending", err)
	}
	if got := gitHere(t, k.Root, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved to %s", got)
	}
}

// #2254: a retried discard that fails again records the paths too.
func TestDiscardUncommitted_RetryFailureRecordsTheLeftover(t *testing.T) {
	k, _ := cleanRemoteKB(t)
	if err := k.WriteFileAtomic("data/left.md", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	gitHere(t, k.Root, "add", "-A")
	lock := filepath.Join(k.Root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	afterDiscardAttempt = func() { os.Remove(lock) }
	beforeDiscardRetry = func() { os.WriteFile(lock, nil, 0o644) }
	t.Cleanup(func() {
		afterDiscardAttempt = func() {}
		beforeDiscardRetry = func() {}
	})
	if err := k.DiscardUncommitted(); err == nil {
		t.Fatal("the retry under a re-planted lock must fail")
	}
	if !k.LeftoversPending() {
		t.Fatal("the failed retry recorded nothing")
	}
}

// #2254: a leftover whose restore fails for a reason other than the lock (a
// symlink) is dropped once the lock is not held, or it would refuse every write
// until a restart.
func TestDiscardLeftovers_DropsARecordTheLockDoesNotExplain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink needs a privilege on Windows")
	}
	k, _ := cleanRemoteKB(t)
	if err := os.Symlink("README.md", filepath.Join(k.Root, "data-link.md")); err != nil {
		t.Fatal(err)
	}
	k.recordLeftovers(map[string]string{"data-link.md": k.fingerprint("data-link.md")})
	k.DiscardLeftovers()
	if k.LeftoversPending() {
		t.Fatal("a restore that fails without a lock kept the record")
	}
	if _, err := k.CommitOpAs("external changes", "", ""); err != nil {
		t.Fatalf("CommitOpAs after the drop: %v", err)
	}
}

// #2254: while the lock is held the record stays, however many times it is tried.
func TestDiscardLeftovers_KeepsTheRecordWhileTheLockIsHeld(t *testing.T) {
	k, _ := cleanRemoteKB(t)
	leftOverUnderLock(t, k)
	k.DiscardLeftovers()
	k.DiscardLeftovers()
	if !k.LeftoversPending() {
		t.Fatal("the record was dropped while the lock was held")
	}
}

// #2254: the reserved-file auto-resolve commits the whole tree and is guarded too.
func TestAutoResolveReserved_RefusesWhileALeftoverIsHeld(t *testing.T) {
	k, _ := cleanRemoteKB(t)
	leftOverUnderLock(t, k)
	err := k.autoResolveReserved(&gitx.RebaseConflictError{LocalSHA: "a", RemoteSHA: "b"})
	if !errors.Is(err, ErrLeftoversPending) {
		t.Fatalf("autoResolveReserved error = %v, want ErrLeftoversPending", err)
	}
}
