package provisioning

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// git runs a real git in dir, so the layouts below are the ones git writes and
// not what this package assumes they look like.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=user", "-c", "user.email=user@example.com",
		"-c", "protocol.file.allow=always", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// gitIgnores asks git itself whether path is ignored in dir.
func gitIgnores(t *testing.T, dir, path string) bool {
	t.Helper()
	err := exec.Command("git", "-C", dir, "check-ignore", "-q", path).Run()
	if err == nil {
		return true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false
	}
	t.Fatalf("git check-ignore %s: %v", path, err)
	return false
}

func commitedRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")
	return repo
}

// roundTrip runs Ensure then Remove against workspace and checks, with git
// itself, that the path is ignored in between and not after.
func roundTrip(t *testing.T, workspace, exclude string) {
	t.Helper()
	if err := EnsureWorkspaceExcluded(workspace, []string{".claude/skills/x"}); err != nil {
		t.Fatalf("EnsureWorkspaceExcluded: %v", err)
	}
	if got := readFile(t, exclude); !strings.Contains(got, "/.claude/skills/x") {
		t.Fatalf("block not in %s:\n%s", exclude, got)
	}
	if !gitIgnores(t, workspace, ".claude/skills/x/SKILL.md") {
		t.Error("git does not ignore the excluded path in the workspace")
	}
	if err := EnsureWorkspaceExcluded(workspace, []string{".claude/skills/x"}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(readFile(t, exclude), excludeMarkerBegin); n != 1 {
		t.Errorf("the block appears %d times after two runs, want 1", n)
	}
	if err := RemoveWorkspaceExclusions(workspace); err != nil {
		t.Fatalf("RemoveWorkspaceExclusions: %v", err)
	}
	if strings.Contains(readFile(t, exclude), excludeMarkerBegin) {
		t.Error("the block survived removal")
	}
	if gitIgnores(t, workspace, ".claude/skills/x/SKILL.md") {
		t.Error("git still ignores the path after removal")
	}
}

// A linked worktree has a `.git` file; info/exclude is the common directory's,
// shared by every worktree of the repository (#1147).
func TestEnsureWorkspaceExcluded_LinkedWorktree(t *testing.T) {
	repo := commitedRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", "-b", "side", wt)
	if st, err := os.Stat(filepath.Join(wt, ".git")); err != nil || !st.Mode().IsRegular() {
		t.Fatalf("worktree .git is not a file: %v", err)
	}
	// Seed a user line: it must survive.
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte("*.swp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, wt, exclude)
	if got := readFile(t, exclude); got != "*.swp\n" {
		t.Errorf("the user's exclude lines changed: %q", got)
	}
}

// A submodule's `.git` file points into the parent's .git/modules, with no
// commondir: that directory holds its own info/exclude.
func TestEnsureWorkspaceExcluded_Submodule(t *testing.T) {
	sub := commitedRepo(t)
	parent := commitedRepo(t)
	git(t, parent, "submodule", "add", "-q", sub, "libs/sub")
	ws := filepath.Join(parent, "libs", "sub")
	exclude := filepath.Join(parent, ".git", "modules", "libs", "sub", "info", "exclude")
	roundTrip(t, ws, exclude)
}

// The two spellings git accepts: a relative gitdir (a submodule written by an
// older git, a relocated worktree), and a relative commondir (what a linked
// worktree's gitdir holds).
func TestEnsureWorkspaceExcluded_HandWrittenGitFile(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	gitdir := filepath.Join(root, "store", "wt1")
	common := filepath.Join(root, "store")
	for _, d := range []string{ws, gitdir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(ws, ".git"), "gitdir: ../store/wt1\r\n")

	// No commondir: the exclude is in the git dir itself.
	if err := EnsureWorkspaceExcluded(ws, []string{".mcp.json"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(gitdir, "info", "exclude")); !strings.Contains(got, "/.mcp.json") {
		t.Errorf("relative gitdir: block missing:\n%s", got)
	}

	// With a relative commondir it moves to the common directory.
	write(filepath.Join(gitdir, "commondir"), "..\n")
	if err := EnsureWorkspaceExcluded(ws, []string{".mcp.json"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(common, "info", "exclude")); !strings.Contains(got, "/.mcp.json") {
		t.Errorf("relative commondir: block missing:\n%s", got)
	}
	if err := RemoveWorkspaceExclusions(ws); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(t, filepath.Join(common, "info", "exclude")), excludeMarkerBegin) {
		t.Error("the block survived removal in the common directory")
	}
}

// A `.git` file that leads nowhere is a warning for the callers, never a crash,
// and nothing is written.
func TestEnsureWorkspaceExcluded_UnparseableGitFileIsAWarning(t *testing.T) {
	for name, content := range map[string]string{
		"no gitdir line": "hello\n",
		"empty target":   "gitdir:\n",
		"missing target": "gitdir: ./nowhere\n",
		"empty file":     "",
	} {
		t.Run(name, func(t *testing.T) {
			ws := t.TempDir()
			if err := os.WriteFile(filepath.Join(ws, ".git"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			before := treeOf(t, ws)
			for _, err := range []error{
				EnsureWorkspaceExcluded(ws, []string{".mcp.json"}),
				RemoveWorkspaceExclusions(ws),
			} {
				if !errors.Is(err, ErrGitDirUnresolvable) || !ExcludeWarning(err) {
					t.Errorf("err = %v, want ErrGitDirUnresolvable", err)
				}
			}
			sameTree(t, "workspace", before, treeOf(t, ws))
		})
	}
}

// The guard still holds in the resolved git directory: a symlinked info/ there
// is refused like in a plain repository (D148).
func TestEnsureWorkspaceExcluded_LinkedWorktreeRefusesASymlinkedInfo(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	gitdir := filepath.Join(root, "gitdir")
	elsewhere := filepath.Join(root, "elsewhere")
	for _, d := range []string{ws, gitdir, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(gitdir, "info")); err != nil {
		t.Fatal(err)
	}
	err := EnsureWorkspaceExcluded(ws, []string{".mcp.json"})
	if !errors.Is(err, ErrSymlinkDestination) || !ExcludeWarning(err) {
		t.Errorf("err = %v, want ErrSymlinkDestination", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("the write followed the link: %v", entries)
	}
}
