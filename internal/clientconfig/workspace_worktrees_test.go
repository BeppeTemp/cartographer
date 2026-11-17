package clientconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// worktreeRepo makes a repository and a linked worktree created outside it,
// both canonical paths.
func worktreeRepo(t *testing.T) (repo, wt string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, wt = filepath.Join(root, "repo", "main"), filepath.Join(root, "repo", "wt1")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "init"}, {"worktree", "add", "-q", "-b", "w", wt}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=u", "GIT_AUTHOR_EMAIL=u@example.com", "GIT_COMMITTER_NAME=u", "GIT_COMMITTER_EMAIL=u@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo, wt
}

// A worktree of a --worktrees binding, created outside the bound folder,
// resolves to the binding (#2196); without the opt-in it stays unbound.
func TestResolveWorkspace_LinkedWorktreeOfWorktreesBinding(t *testing.T) {
	repo, wt := worktreeRepo(t)
	sub := filepath.Join(wt, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, optIn := range []bool{true, false} {
		cfg := &Config{}
		if err := cfg.BindWorkspaceWith("claude", repo, []string{"kb-b"}, BindOptions{Worktrees: optIn}); err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{wt, sub} {
			got, ok, err := cfg.ResolveWorkspace("claude", dir)
			if err != nil || ok != optIn {
				t.Fatalf("optIn=%v dir=%s: ok=%v err=%v", optIn, dir, ok, err)
			}
			if optIn && (len(got.KBs) != 1 || got.KBs[0] != "kb-b") {
				t.Errorf("KBs = %v, want [kb-b]", got.KBs)
			}
		}
	}
}

// A failed listing resolves the directory as unbound, never as bound.
func TestResolveWorkspace_WorktreeListingFailureIsUnbound(t *testing.T) {
	repo, wt := worktreeRepo(t)
	prev := LinkedWorktrees
	LinkedWorktrees = func(string) ([]string, error) { return nil, os.ErrPermission }
	t.Cleanup(func() { LinkedWorktrees = prev })
	cfg := &Config{}
	if err := cfg.BindWorkspaceWith("claude", repo, []string{"kb-b"}, BindOptions{Worktrees: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cfg.ResolveWorkspace("claude", wt); ok || err != nil {
		t.Fatalf("ok=%v err=%v, want unbound", ok, err)
	}
}
