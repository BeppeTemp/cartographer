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

func TestSyncOut_Disabled_NoOp(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.GitSync = false // disabled → no-op

	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut disabled must be a no-op, err: %v", err)
	}
	// The bare remote must not have received any branch.
	out := gitHere(t, bare, "branch", "--list")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("bare should have no branch with GitSync=false, has: %q", out)
	}
}

func TestSyncOut_PushesToRemote(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.GitSync = true

	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	branch, _ := gitx.Branch(k.Root)
	count := gitHere(t, bare, "rev-list", "--count", branch)
	if count == "" || count == "0" {
		t.Fatalf("the bare remote received no commits on branch %s (count=%q)", branch, count)
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
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.GitSync = true
	// Seed the remote so a concurrent clone has a branch to work with.
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	branch, _ := gitx.Branch(k.Root)

	k.SyncInWindow = 30 * time.Second
	if err := k.SyncIn(); err != nil {
		t.Fatalf("first SyncIn: %v", err)
	}
	if k.lastSyncIn.IsZero() {
		t.Fatalf("first SyncIn should have set lastSyncIn")
	}

	pushCommitToBare(t, bare, branch, "remote-change.txt")

	// Second SyncIn, immediately after the first: within the freshness
	// window, so it must be a no-op and NOT pull the remote change down.
	if err := k.SyncIn(); err != nil {
		t.Fatalf("second SyncIn (within window): %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "remote-change.txt")); err == nil {
		t.Fatalf("second SyncIn within the freshness window pulled the remote change; it should have been a no-op")
	}

	// Simulate the window having elapsed by rewinding lastSyncIn: SyncIn
	// must now actually fetch and pull the remote change.
	k.lastSyncIn = time.Now().Add(-31 * time.Second)
	if err := k.SyncIn(); err != nil {
		t.Fatalf("third SyncIn (window elapsed): %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "remote-change.txt")); err != nil {
		t.Fatalf("third SyncIn after the window elapsed should have pulled the remote change: %v", err)
	}
}

func TestSyncIn_WindowZero_AlwaysFetches(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.GitSync = true
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	branch, _ := gitx.Branch(k.Root)

	k.SyncInWindow = 0 // current behavior: sync on every call
	callbackCalls := 0
	k.OnSyncIn = func() { callbackCalls++ }
	if err := k.SyncIn(); err != nil {
		t.Fatalf("first SyncIn: %v", err)
	}
	if callbackCalls != 0 {
		t.Fatalf("OnSyncIn called %d times without a HEAD change", callbackCalls)
	}

	pushCommitToBare(t, bare, branch, "remote-change.txt")

	if err := k.SyncIn(); err != nil {
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
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	base := t.TempDir()
	k, err := Init(filepath.Join(base, "kb"))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	k.GitSync = true // enabled but with no remote → still a no-op

	if err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn without a remote must be a no-op, err: %v", err)
	}
}
