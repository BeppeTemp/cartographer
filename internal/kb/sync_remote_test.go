package kb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

func gitHere(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func haveGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// setupKBWithRemote initialises a KB (git repo + initial commit via Init) and
// attaches a bare remote as "origin".
func setupKBWithRemote(t *testing.T) (k *KB, bare string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "kb")
	var err error
	k, err = Init(root)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	bare = filepath.Join(base, "remote.git")
	gitHere(t, base, "init", "--bare", bare)
	if err := gitx.AddRemote(root, "origin", bare); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	return k, bare
}

func TestSyncOut_PushesToRemote(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)

	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	branch, _ := gitx.Branch(k.Root)
	count := gitHere(t, bare, "rev-list", "--count", branch)
	if count == "" || count == "0" {
		t.Fatalf("the bare remote received no commits on branch %s (count=%q)", branch, count)
	}
}

func TestSyncOut_DetachedHeadRecordsFailure(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := setupKBWithRemote(t)
	gitHere(t, k.Root, "checkout", "--detach")
	if err := k.SyncOut(); err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("SyncOut detached HEAD error = %v", err)
	}
	if s := k.GitStatusSnapshot(); s.State != "failed" || s.Attempts != 1 || !strings.Contains(s.LastError, "detached HEAD") {
		t.Fatalf("status = %+v", s)
	}
}

func TestGitStatusSnapshot_MissingRemoteTrackingRefHasUnknownAheadCount(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := setupKBWithRemote(t)
	if s := k.GitStatusSnapshot(); s.UnpushedCommits != nil {
		t.Fatalf("unpushed commits = %v, want nil without origin tracking ref", *s.UnpushedCommits)
	}
	if s := k.GitStatusSnapshot(); s.BehindCommits != nil || s.LastPushAt != nil || s.LastSyncInAt != nil {
		t.Fatalf("behind/last_push/last_sync_in = %v/%v/%v, want all nil", s.BehindCommits, s.LastPushAt, s.LastSyncInAt)
	}
}

func TestSyncOut_PushFailureRecordsAllAttemptsWithoutBackoff(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}
	oldAttempts, oldBackoff, oldSleep := syncOutMaxAttempts, syncOutInitialBackoff, syncOutSleep
	syncOutMaxAttempts, syncOutInitialBackoff, syncOutSleep = 3, 0, func(time.Duration) {}
	t.Cleanup(func() { syncOutMaxAttempts, syncOutInitialBackoff, syncOutSleep = oldAttempts, oldBackoff, oldSleep })
	if err := k.SyncOut(); err == nil {
		t.Fatal("SyncOut push rejection succeeded")
	}
	if s := k.GitStatusSnapshot(); s.State != "failed" || s.Attempts != 3 || s.LastError == "" {
		t.Fatalf("status = %+v", s)
	}
}

// gitIn runs git with dir as its working directory (as opposed to gitHere,
// which uses "-C dir" and therefore cannot be used to run "clone" into a
// not-yet-existing target directory).
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (dir=%s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// pushCommitToBare simulates a concurrent writer: clones bare into a scratch
// dir, adds a file on branch, commits and pushes it.
func pushCommitToBare(t *testing.T, bare, branch, file string) {
	t.Helper()
	parent := t.TempDir()
	scratch := filepath.Join(parent, "clone")
	gitIn(t, parent, "clone", bare, scratch)
	gitIn(t, scratch, "checkout", branch)
	if err := os.WriteFile(filepath.Join(scratch, file), []byte("remote change\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	gitIn(t, scratch, "add", file)
	gitIn(t, scratch, "-c", "user.email=test@test", "-c", "user.name=test", "commit", "-m", "remote change")
	gitIn(t, scratch, "push", "origin", branch)
}

func TestSyncIn_FreshnessWindow_SkipsRedundantFetch(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	// Seed the remote so a concurrent clone has a branch to work with.
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	branch, _ := gitx.Branch(k.Root)

	k.SyncInWindow = 30 * time.Second
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("first SyncIn: %v", err)
	}
	if k.lastSyncInAt().IsZero() {
		t.Fatalf("first SyncIn should have set lastSyncIn")
	}

	pushCommitToBare(t, bare, branch, "remote-change.txt")

	// Second SyncIn, immediately after the first: within the freshness
	// window, so it must be a no-op and NOT pull the remote change down.
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("second SyncIn (within window): %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "remote-change.txt")); err == nil {
		t.Fatalf("second SyncIn within the freshness window pulled the remote change; it should have been a no-op")
	}

	// Simulate the window having elapsed by rewinding lastSyncIn: SyncIn
	// must now actually fetch and pull the remote change.
	k.setLastSyncIn(time.Now().Add(-31 * time.Second))
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("third SyncIn (window elapsed): %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "remote-change.txt")); err != nil {
		t.Fatalf("third SyncIn after the window elapsed should have pulled the remote change: %v", err)
	}
}

func TestSyncIn_WindowZero_AlwaysFetches(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	branch, _ := gitx.Branch(k.Root)

	k.SyncInWindow = 0 // current behavior: sync on every call
	callbackCalls := 0
	k.OnSyncIn = func() { callbackCalls++ }
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("first SyncIn: %v", err)
	}
	if callbackCalls != 0 {
		t.Fatalf("OnSyncIn called %d times without a HEAD change", callbackCalls)
	}

	pushCommitToBare(t, bare, branch, "remote-change.txt")

	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("second SyncIn: %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "remote-change.txt")); err != nil {
		t.Fatalf("second SyncIn with window=0 should have pulled the remote change: %v", err)
	}
	if callbackCalls != 1 {
		t.Fatalf("OnSyncIn called %d times after a pulled HEAD change, want 1", callbackCalls)
	}
}

func TestSyncIn_NoRemote_NoOp(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	base := t.TempDir()
	k, err := Init(filepath.Join(base, "kb"))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn without a remote must be a no-op, err: %v", err)
	}
}

// D713: last_push_at survives a later failed push, and behind counts against
// the remote-tracking ref as the last fetch left it.
func TestGitStatusSnapshot_BehindAndLastPush(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	s := k.GitStatusSnapshot()
	if s.LastPushAt == nil || s.BehindCommits == nil || *s.BehindCommits != 0 {
		t.Fatalf("after push: last_push_at=%v behind=%v", s.LastPushAt, s.BehindCommits)
	}
	first := *s.LastPushAt

	// A second clone pushes one commit; fetching alone makes k one behind.
	// Clone k's branch explicitly: the bare remote's HEAD follows the host's
	// init.defaultBranch, which need not match the branch k pushed.
	branch, err := gitx.Branch(k.Root)
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", "-b", branch, bare, other)
	gitHere(t, other, "config", "user.email", "user@example.com")
	gitHere(t, other, "config", "user.name", "user")
	if err := os.WriteFile(filepath.Join(other, "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitHere(t, other, "add", ".")
	gitHere(t, other, "commit", "-q", "-m", "extra")
	gitHere(t, other, "push", "-q", "origin", "HEAD")
	if err := gitx.Fetch(k.Root, "origin"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if s := k.GitStatusSnapshot(); s.BehindCommits == nil || *s.BehindCommits != 1 {
		t.Fatalf("behind = %v, want 1", s.BehindCommits)
	}

	// A failed push must not erase the last successful one.
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}
	oldAttempts, oldBackoff, oldSleep := syncOutMaxAttempts, syncOutInitialBackoff, syncOutSleep
	syncOutMaxAttempts, syncOutInitialBackoff, syncOutSleep = 1, 0, func(time.Duration) {}
	t.Cleanup(func() { syncOutMaxAttempts, syncOutInitialBackoff, syncOutSleep = oldAttempts, oldBackoff, oldSleep })
	if err := k.SyncOut(); err == nil {
		t.Fatal("SyncOut to a removed remote succeeded")
	}
	s = k.GitStatusSnapshot()
	if s.State != "failed" || s.LastPushAt == nil || !s.LastPushAt.Equal(first) {
		t.Fatalf("after failed push: state=%s last_push_at=%v want %v", s.State, s.LastPushAt, first)
	}
}

func TestSyncIn_RecordsLastSyncInAt(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := setupKBWithRemote(t)
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	s := k.GitStatusSnapshot()
	if s.LastSyncInAt == nil {
		t.Fatal("last_sync_in_at is nil after a successful SyncIn")
	}
	if s.LastSyncInAt.Location() != time.UTC {
		t.Fatalf("last_sync_in_at location = %v, want UTC", s.LastSyncInAt.Location())
	}
	if s.LastPushAt != nil && s.LastPushAt.Location() != time.UTC {
		t.Fatalf("last_push_at location = %v, want UTC", s.LastPushAt.Location())
	}

	// Issue #1170: setLastSyncIn must normalize to UTC even if passed a local offset.
	cest := time.FixedZone("CEST", 2*60*60)
	k.setLastSyncIn(time.Now().In(cest))
	if got := k.GitStatusSnapshot().LastSyncInAt; got == nil || got.Location() != time.UTC {
		t.Fatalf("setLastSyncIn with local time: got location %v, want UTC", got.Location())
	}
}
