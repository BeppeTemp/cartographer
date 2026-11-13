package kb

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// leaveIndexLock plants the index.lock a killed git leaves, an hour old.
func leaveIndexLock(t *testing.T, k *KB) string {
	t.Helper()
	lock := filepath.Join(k.Root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	return lock
}

func writeOne(t *testing.T, k *KB, rel string) error {
	t.Helper()
	if err := k.WriteFileAtomic(rel, []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	_, err := k.CommitOpAs("write "+rel, "", "")
	return err
}

// A lock left by a crash is cleared when the server opens the KB, and the first
// write after it commits (#1997).
func TestClearStaleIndexLock_StaleLockAtOpenIsRemovedAndTheWriteSucceeds(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := mustInitKB(t)
	lock := leaveIndexLock(t, k)

	k.ClearStaleIndexLock()

	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("the stale index.lock is still there: %v", err)
	}
	if err := writeOne(t, k, "data/a.md"); err != nil {
		t.Fatalf("the first write after the clean-up failed: %v", err)
	}
}

// A lock that appears after open may be an operator's own git: it is kept, and the
// write error names the file and says when to remove it.
func TestClearStaleIndexLock_LockAfterOpenIsKeptAndNamedInTheWriteError(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := mustInitKB(t)
	k.ClearStaleIndexLock()
	lock := leaveIndexLock(t, k)

	err := writeOne(t, k, "data/a.md")
	if err == nil {
		t.Fatal("the write succeeded with an index.lock in the clone")
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Fatalf("a lock that appeared after open was removed: %v", statErr)
	}
	for _, want := range []string{"index.lock", "if no git process runs in", "remove that file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the write error lacks %q:\n%v", want, err)
		}
	}
	if !namesLock(err.Error(), lock) {
		t.Errorf("the write error does not name %s:\n%v", lock, err)
	}
}

// Another process holding the KB's lock may have a git running in the clone: nothing
// is removed.
func TestClearStaleIndexLock_LeavesTheLockWhileAnotherHolderHasTheKB(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := mustInitKB(t)
	lock := leaveIndexLock(t, k)
	release, err := k.AcquireProcessLock(time.Second, "other process")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	k.ClearStaleIndexLock()

	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("the lock was removed while the KB was held elsewhere: %v", err)
	}
}

// The skip is not silent: the leftover stays until the next start, so one line says
// that a lock exists and why it was not judged. Not parallel: it swaps os.Stderr.
func TestClearStaleIndexLock_LogsTheSkipWhenAnotherHolderHasTheKB(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := mustInitKB(t)
	lock := leaveIndexLock(t, k)
	release, err := k.AcquireProcessLock(time.Second, "other process")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	k.ClearStaleIndexLock()
	os.Stderr = saved
	w.Close()
	logged, _ := io.ReadAll(r)

	for _, want := range []string{"index.lock", "skipped", "another process holds the KB", k.Root} {
		if !strings.Contains(string(logged), want) {
			t.Errorf("the log lacks %q:\n%s", want, logged)
		}
	}
	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("the lock was removed: %v", err)
	}

	// Without a lock, a held KB logs nothing.
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	r, w, _ = os.Pipe()
	os.Stderr = w
	k.ClearStaleIndexLock()
	os.Stderr = saved
	w.Close()
	if logged, _ := io.ReadAll(r); len(logged) != 0 {
		t.Errorf("logged with no lock present: %s", logged)
	}
}

// A write on a KB with a remote first rebases onto what the remote has gained: with a
// leftover lock git words that "could not detach HEAD", and it was reported as a
// conflict with no files. The error names the lock.
func TestSyncIn_LeftoverIndexLockIsNamedNotReportedAsAConflict(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := cleanRemoteKB(t)
	writeAndCommit(t, k, "data/local.md")
	pushCommitToBare(t, bare, "main", "from-remote.md")
	lock := leaveIndexLock(t, k)

	_, err := k.SyncIn()
	if err == nil {
		t.Fatal("SyncIn succeeded with an index.lock in the clone")
	}
	var rce *gitx.RebaseConflictError
	if errors.As(err, &rce) {
		t.Fatalf("a leftover lock was reported as a rebase conflict: %v", err)
	}
	if !namesLock(err.Error(), lock) || !strings.Contains(err.Error(), "if no git process runs in") {
		t.Errorf("the error does not name the lock and the remedy:\n%v", err)
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Errorf("the lock was removed: %v", statErr)
	}
}

// namesLock reports whether msg names the lock file. git prints the path with
// forward slashes and, on Windows, spells it in the long form (runneradmin) where
// t.TempDir() gives the 8.3 short one (RUNNER~1): the lock is also tried resolved,
// which expands the short names.
func namesLock(msg, lock string) bool {
	msg = filepath.ToSlash(msg)
	if strings.Contains(msg, filepath.ToSlash(lock)) {
		return true
	}
	real, err := filepath.EvalSymlinks(lock)
	return err == nil && strings.Contains(msg, filepath.ToSlash(real))
}
