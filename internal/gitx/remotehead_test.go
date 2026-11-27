package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

func headTestRepo(t *testing.T) string {
	t.Helper()
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := filepath.Join(t.TempDir(), "r")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "init", "-q")
	return dir
}

func TestRemoteHeadBranch(t *testing.T) {
	dir := headTestRepo(t)
	if b, ok := RemoteHeadBranch(dir, "origin"); ok || b != "" {
		t.Fatalf("absent origin/HEAD = %q, %v", b, ok)
	}
	mustGit(t, dir, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "--allow-empty", "-m", "c")
	mustGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	mustGit(t, dir, "update-ref", "refs/remotes/origin/feat/x", "HEAD")
	mustGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	if b, ok := RemoteHeadBranch(dir, ""); !ok || b != "main" {
		t.Fatalf("origin/HEAD = %q, %v, want main (an empty remote name is origin)", b, ok)
	}
	mustGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/feat/x")
	if b, ok := RemoteHeadBranch(dir, "origin"); !ok || b != "feat/x" {
		t.Fatalf("origin/HEAD = %q, %v, want feat/x", b, ok)
	}
	// Another remote's HEAD is not this one's.
	if b, ok := RemoteHeadBranch(dir, "other"); ok {
		t.Fatalf("remote other = %q", b)
	}
	// A symref that points outside refs/remotes/origin/ (the prefix does not
	// match) and one that is empty after the prefix are not a branch.
	mustGit(t, dir, "update-ref", "refs/remotes/upstream/main", "HEAD")
	mustGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/upstream/main")
	if b, ok := RemoteHeadBranch(dir, "origin"); ok {
		t.Fatalf("a ref outside refs/remotes/origin/ read as branch %q", b)
	}
	// git refuses to create it, so write the damaged file by hand.
	if err := os.WriteFile(filepath.Join(dir, ".git", "refs", "remotes", "origin", "HEAD"), []byte("ref: refs/remotes/origin/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, ok := RemoteHeadBranch(dir, "origin"); ok {
		t.Fatalf("an empty branch read as %q", b)
	}
}

func TestRemoteURLIfConfigured(t *testing.T) {
	dir := headTestRepo(t)
	if u, exists, err := RemoteURLIfConfigured(dir, "origin"); err != nil || exists || u != "" {
		t.Fatalf("not configured = %q, %v, %v (exit 1 is absence, not an error)", u, exists, err)
	}
	mustGit(t, dir, "remote", "add", "origin", "https://git.example.com/org/core.git")
	if u, exists, err := RemoteURLIfConfigured(dir, "origin"); err != nil || !exists || u != "https://git.example.com/org/core.git" {
		t.Fatalf("configured = %q, %v, %v", u, exists, err)
	}
	// A git that cannot answer is an error, never "no remote".
	if err := os.Rename(filepath.Join(dir, ".git"), filepath.Join(dir, ".git-away")); err != nil {
		t.Fatal(err)
	}
	if u, exists, err := RemoteURLIfConfigured(dir, "origin"); err == nil {
		t.Fatalf("a missing repository read as %q, %v", u, exists)
	}
}

// An absent origin/HEAD is "not known" with no error; a git failure is an error,
// so a caller does not read a corrupt or unreadable repository as "before the
// first sync".
func TestRemoteHeadBranchErrTellsAbsentFromAFailure(t *testing.T) {
	dir := headTestRepo(t)
	if b, ok, err := RemoteHeadBranchErr(dir, "origin"); ok || b != "" || err != nil {
		t.Fatalf("absent origin/HEAD = %q, %v, %v; want not known, no error", b, ok, err)
	}
	notARepo := t.TempDir()
	if _, ok, err := RemoteHeadBranchErr(filepath.Join(notARepo, "missing"), "origin"); ok || err == nil {
		t.Fatalf("a directory git cannot read = ok %v, err %v; want an error", ok, err)
	}
}
