package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// remoteWithDefault creates a bare remote whose default branch is main and
// that already holds one commit on it, then clones it into a KB directory.
func remoteWithDefault(t *testing.T) (k *KB, bare string) {
	t.Helper()
	base := t.TempDir()
	bare = filepath.Join(base, "remote.git")
	gitIn(t, base, "-c", "init.defaultBranch=main", "init", "-q", "--bare", bare)
	seed := filepath.Join(base, "seed")
	gitIn(t, base, "clone", "-q", bare, seed)
	gitIn(t, seed, "checkout", "-q", "-B", "main")
	if err := os.WriteFile(filepath.Join(seed, "seed.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", "seed.md")
	gitIn(t, seed, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "seed")
	gitIn(t, seed, "push", "-q", "origin", "main")
	root := filepath.Join(base, "kb")
	gitIn(t, base, "clone", "-q", bare, root)
	k, err := Init(root)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	k.GitSync, k.AutoCommit = true, true
	return k, bare
}

func remoteHeads(t *testing.T, bare string) string {
	t.Helper()
	return gitHere(t, bare, "for-each-ref", "--format=%(refname)", "refs/heads/")
}

// WP1: a clone of an empty remote whose host named the unborn branch
// "master" ends up exactly like `kb create`: on main, with one commit.
func TestInit_EmptyCloneLandsOnMainWithInitialCommit(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	gitIn(t, base, "-c", "init.defaultBranch=master", "init", "-q", "--bare", bare)
	root := filepath.Join(base, "kb")
	gitIn(t, base, "-c", "init.defaultBranch=master", "clone", "-q", bare, root)
	if _, err := Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if b, _ := gitx.Branch(root); b != gitx.DefaultBranch {
		t.Fatalf("branch = %q, want %q", b, gitx.DefaultBranch)
	}
	if n := gitHere(t, root, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("commits = %s, want 1", n)
	}
}

func TestInit_ExistingCommitsOnMasterStayOnMaster(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	root := filepath.Join(t.TempDir(), "kb")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "-c", "init.defaultBranch=master", "init", "-q")
	gitIn(t, root, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "--allow-empty", "-m", "seed")
	if _, err := Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if b, _ := gitx.Branch(root); b != "master" {
		t.Fatalf("branch = %q, want master kept", b)
	}
	if n := gitHere(t, root, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("commits = %s, want the existing one only", n)
	}
}

// WP2: a clone on the remote default branch syncs and pushes as before.
func TestSync_OnDefaultBranchWrites(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if err := k.WriteFileAtomic("data/a.md", []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("write a"); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	if got := gitHere(t, bare, "rev-parse", "main"); got != gitHere(t, k.Root, "rev-parse", "HEAD") {
		t.Fatalf("remote main %s is not the KB HEAD", got)
	}
	s := k.GitStatusSnapshot()
	if s.Branch != "main" || s.RemoteDefaultBranch != "main" || s.State != "clean" {
		t.Fatalf("status = %+v", s)
	}
}

// WP2: a clone checked out on another branch is refused before anything is
// committed, and no branch appears on the remote.
func TestSyncIn_DivergedBranchRefused(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	gitHere(t, k.Root, "checkout", "-q", "-b", "feature")
	headBefore := gitHere(t, k.Root, "rev-parse", "HEAD")

	_, err := k.SyncIn()
	if !errors.Is(err, ErrBranchDiverged) {
		t.Fatalf("SyncIn err = %v, want ErrBranchDiverged", err)
	}
	for _, want := range []string{`"feature"`, `"main"`, k.Root, "restart"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %s", err, want)
		}
	}
	if got := gitHere(t, k.Root, "rev-parse", "HEAD"); got != headBefore {
		t.Fatal("SyncIn moved HEAD on a diverged branch")
	}
	s := k.GitStatusSnapshot()
	if s.State != "degraded" || s.Branch != "feature" || s.RemoteDefaultBranch != "main" || !strings.Contains(s.LastError, "feature") {
		t.Fatalf("status = %+v", s)
	}
	if heads := remoteHeads(t, bare); heads != "refs/heads/main" {
		t.Fatalf("remote heads = %q, want main only", heads)
	}

	// Recovery: back on main, the next sync clears the degraded status.
	gitHere(t, k.Root, "checkout", "-q", "main")
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn after recovery: %v", err)
	}
	if s := k.GitStatusSnapshot(); s.State != "clean" || s.LastError != "" {
		t.Fatalf("status after recovery = %+v", s)
	}
}

// WP2: an empty remote has nothing to pull; SyncIn succeeds without error.
func TestSyncIn_EmptyRemoteIsNoOp(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := setupKBWithRemote(t)
	k.GitSync = true
	headBefore := gitHere(t, k.Root, "rev-parse", "HEAD")
	fetched, err := k.SyncIn()
	if err != nil || !fetched {
		t.Fatalf("SyncIn on empty remote = (%v, %v), want (true, nil)", fetched, err)
	}
	if got := gitHere(t, k.Root, "rev-parse", "HEAD"); got != headBefore {
		t.Fatal("SyncIn on an empty remote moved HEAD")
	}
}

// WP3: the first push of main to an empty remote creates it with upstream.
func TestSyncOut_EmptyRemoteFirstPushCreatesMain(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.GitSync = true
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	if heads := remoteHeads(t, bare); heads != "refs/heads/main" {
		t.Fatalf("remote heads = %q, want main", heads)
	}
	if up := gitHere(t, k.Root, "rev-parse", "--abbrev-ref", "main@{upstream}"); up != "origin/main" {
		t.Fatalf("upstream = %q, want origin/main", up)
	}
}

// WP3: the first push to an empty remote only ever creates main.
func TestSyncOut_EmptyRemoteRefusesOtherBranch(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.GitSync = true
	gitHere(t, k.Root, "branch", "-m", "master")
	err := k.SyncOut()
	if !errors.Is(err, ErrBranchDiverged) || !strings.Contains(err.Error(), "remote is empty") {
		t.Fatalf("SyncOut err = %v", err)
	}
	if heads := remoteHeads(t, bare); heads != "" {
		t.Fatalf("remote heads = %q, want none", heads)
	}
	if s := k.GitStatusSnapshot(); s.State != "degraded" {
		t.Fatalf("status = %+v", s)
	}
}

// WP3: SyncOut never creates a branch on a non-empty remote, even when
// SyncIn was bypassed (here: never run), and the local commit is kept.
func TestSyncOut_NeverCreatesRemoteBranch(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	gitHere(t, k.Root, "checkout", "-q", "-b", "feature")
	if err := k.WriteFileAtomic("data/b.md", []byte("b\n")); err != nil {
		t.Fatal(err)
	}
	sha, err := k.CommitOp("write b")
	if err != nil || sha == "" {
		t.Fatalf("CommitOp = (%q, %v)", sha, err)
	}
	err = k.SyncOut()
	if !errors.Is(err, ErrBranchDiverged) {
		t.Fatalf("SyncOut err = %v, want ErrBranchDiverged", err)
	}
	if heads := remoteHeads(t, bare); heads != "refs/heads/main" {
		t.Fatalf("remote heads = %q, want main only", heads)
	}
	if got := gitHere(t, k.Root, "rev-parse", "HEAD"); got != sha {
		t.Fatal("the local commit was lost")
	}
	if s := k.GitStatusSnapshot(); s.State != "degraded" || s.RemoteDefaultBranch != "main" {
		t.Fatalf("status = %+v", s)
	}
}

// WP3: a remote whose HEAD names no existing branch (bare repo initialised
// with master, main pushed) still has main as the canonical branch.
func TestSyncIn_DanglingRemoteHeadFallsBackToMain(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	gitHere(t, bare, "symbolic-ref", "HEAD", "refs/heads/master")
	gitHere(t, k.Root, "checkout", "-q", "-b", "feature")
	if _, err := k.SyncIn(); !errors.Is(err, ErrBranchDiverged) {
		t.Fatalf("SyncIn err = %v, want ErrBranchDiverged", err)
	}
	if got := k.RemoteDefaultBranch(); got != "main" {
		t.Fatalf("RemoteDefaultBranch = %q, want main", got)
	}
}
