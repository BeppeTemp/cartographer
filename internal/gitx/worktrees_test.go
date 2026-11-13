package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseWorktreeList(t *testing.T) {
	out := "worktree /repo/main\nHEAD aaa\nbranch refs/heads/main\n\n" +
		"worktree /repo/linked one\nHEAD bbb\nbranch refs/heads/x\n\n" +
		"worktree /repo/det\nHEAD ccc\ndetached\n\n" +
		"worktree /repo/bare\nbare\n\n" +
		"worktree /repo/gone\nHEAD ddd\nbranch refs/heads/y\nprunable gitdir file points to non-existent location\n"
	got := parseWorktreeList(out)
	want := []string{"/repo/linked one", "/repo/det"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parse = %q, want %q", got, want)
	}
	if got := parseWorktreeList(""); len(got) != 0 {
		t.Fatalf("empty output = %q", got)
	}
}

func TestFilterLinkedWorktrees(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	inGit := filepath.Join(a, ".git", "wt")
	for _, d := range []string{a, b, inGit} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := filterLinkedWorktrees([]string{b, a, a, inGit, filepath.Join(root, "missing"), "rel", root}, root)
	if want := []string{a, b}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filter = %q, want %q", got, want)
	}
}

func TestLinkedWorktreesReal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-q", "-b", "main")
	mustGit(t, repo, "-c", "user.name=u", "-c", "user.email=u@example.com", "commit", "-q", "--allow-empty", "-m", "i")
	wt := filepath.Join(root, "wt")
	mustGit(t, repo, "worktree", "add", "-q", "-b", "side", wt)

	got, err := LinkedWorktrees(repo)
	if err != nil || !reflect.DeepEqual(got, []string{wt}) {
		t.Fatalf("from main = %q, %v; want [%s]", got, err, wt)
	}
	// Bound at the linked worktree itself: the main tree is the first record
	// and dropped, the bound folder is dropped, nothing is left.
	got, _ = LinkedWorktrees(wt)
	if len(got) != 0 {
		t.Fatalf("from linked = %q, want none", got)
	}
	if got, err := LinkedWorktrees(root); err != nil || got != nil {
		t.Fatalf("not a repository = %q, %v; want nil, nil", got, err)
	}
}
