package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// z joins fields as `git worktree list --porcelain -z` does: NUL after every
// field and an empty one after every record.
func z(records ...[]string) string {
	var b strings.Builder
	for _, r := range records {
		for _, f := range r {
			b.WriteString(f + "\x00")
		}
		b.WriteString("\x00")
	}
	return b.String()
}

func TestParseWorktreeList(t *testing.T) {
	out := z(
		[]string{"worktree /repo/main", "HEAD aaa", "branch refs/heads/main"},
		[]string{"worktree /repo/linked one", "HEAD bbb", "branch refs/heads/x"},
		[]string{"worktree /repo/det", "HEAD ccc", "detached"},
		[]string{"worktree /repo/bare", "bare"},
		[]string{"worktree /repo/gone", "HEAD ddd", "branch refs/heads/y", "prunable gitdir file points to non-existent location"},
	)
	got := parseWorktreeList(out)
	// The main tree is kept: filterLinkedWorktrees drops the bound folder.
	want := []string{"/repo/main", "/repo/linked one", "/repo/det"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parse = %q, want %q", got, want)
	}
	if got := parseWorktreeList(""); len(got) != 0 {
		t.Fatalf("empty output = %q", got)
	}
}

// A newline in a path must not forge a record, and a path with a control
// character is rejected.
func TestParseWorktreeListRejectsForgedAndControl(t *testing.T) {
	forged := z(
		[]string{"worktree /repo/main", "HEAD aaa"},
		[]string{"worktree /repo/evil\nworktree /victim", "HEAD bbb"},
		[]string{"worktree /repo/tab\tname", "HEAD ccc"},
		[]string{"worktree /repo/esc\x1bname", "HEAD ddd"},
		[]string{"worktree /repo/ok", "HEAD eee"},
	)
	got := parseWorktreeList(forged)
	if want := []string{"/repo/main", "/repo/ok"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("parse = %q, want %q", got, want)
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
	// Bound at the linked worktree itself: the bound folder is dropped and the
	// main checkout is a sibling that gets the projection.
	got, err = LinkedWorktrees(wt)
	if err != nil || !reflect.DeepEqual(got, []string{repo}) {
		t.Fatalf("from linked = %q, %v; want [%s]", got, err, repo)
	}
	if got, err := LinkedWorktrees(root); err != nil || got != nil {
		t.Fatalf("not a repository = %q, %v; want nil, nil", got, err)
	}
}

// A repository whose listing fails for a reason other than "not a repository"
// is an error, never an empty list: the caller would prune on it.
func TestLinkedWorktreesRealFailureIsAnError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-q", "-b", "main")
	// A config git cannot parse makes every command in the repository fail.
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("[core\nbroken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LinkedWorktrees(repo)
	if err == nil || got != nil {
		t.Fatalf("broken repository = %q, %v; want an error", got, err)
	}
}
