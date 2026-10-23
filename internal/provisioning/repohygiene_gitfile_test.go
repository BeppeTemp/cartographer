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
	if n := strings.Count(readFile(t, exclude), "# >>> cartographer (managed"); n != 1 {
		t.Errorf("the block appears %d times after two runs, want 1", n)
	}
	if err := RemoveWorkspaceExclusions(workspace); err != nil {
		t.Fatalf("RemoveWorkspaceExclusions: %v", err)
	}
	if strings.Contains(readFile(t, exclude), "cartographer (managed") {
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

// mkGitDir makes a directory that looks like a git directory to the resolver.
func mkGitDir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "HEAD"), "ref: refs/heads/main\n")
	return dir
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The spellings git accepts that a real `git` here does not produce: a relative
// gitdir (a submodule written by an older git), and a relative commondir with a
// back-link (what a linked worktree's git directory holds), CRLF included.
func TestEnsureWorkspaceExcluded_HandWrittenGitFile(t *testing.T) {
	root := t.TempDir()

	parent := filepath.Join(root, "parent")
	mkGitDir(t, filepath.Join(parent, ".git"))
	subGit := mkGitDir(t, filepath.Join(parent, ".git", "modules", "sub"))
	writeTestFile(t, filepath.Join(subGit, "config"), "[core]\n\tbare = false\n\tworktree = ../../../sub\n")
	sub := filepath.Join(parent, "sub")
	writeTestFile(t, filepath.Join(sub, ".git"), "gitdir: ../.git/modules/sub\r\n")
	if err := EnsureWorkspaceExcluded(sub, []string{".mcp.json"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(subGit, "info", "exclude")); !strings.Contains(got, "/.mcp.json") || !strings.Contains(got, excludeMarkerBegin) {
		t.Errorf("relative gitdir: unkeyed block missing:\n%s", got)
	}

	common := mkGitDir(t, filepath.Join(root, "store"))
	wtGit := mkGitDir(t, filepath.Join(common, "worktrees", "wt1"))
	ws := filepath.Join(root, "ws")
	writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: ../store/worktrees/wt1\n")
	writeTestFile(t, filepath.Join(wtGit, "commondir"), "../..\n")
	writeTestFile(t, filepath.Join(wtGit, "gitdir"), filepath.Join(ws, ".git")+"\n")
	if err := EnsureWorkspaceExcluded(ws, []string{".mcp.json"}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(common, "info", "exclude"))
	if begin, _ := blockMarkers("wt1"); !strings.Contains(got, begin) || !strings.Contains(got, "/.mcp.json") {
		t.Errorf("relative commondir: keyed block missing:\n%s", got)
	}
	if err := RemoveWorkspaceExclusions(ws); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(common, "info", "exclude")); strings.Contains(got, "cartographer") {
		t.Errorf("the keyed block survived removal:\n%s", got)
	}
}

// A `.git` file that leads nowhere is a warning for the callers, never a crash,
// and nothing is written. Its content stays out of the message.
func TestEnsureWorkspaceExcluded_UnparseableGitFileIsAWarning(t *testing.T) {
	for name, content := range map[string]string{
		"no gitdir line": "hello secret-token\n",
		"empty target":   "gitdir:\n",
		"missing target": "gitdir: ./nowhere-secret\n",
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
					t.Fatalf("err = %v, want ErrGitDirUnresolvable", err)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Errorf("the error leaks the pointer's content: %v", err)
				}
			}
			sameTree(t, "workspace", before, treeOf(t, ws))
		})
	}
}

// A `.git` file is untrusted: a target that is not this workspace's own git
// directory is refused before anything is written (it is the repository's
// content, not the user's).
func TestEnsureWorkspaceExcluded_RefusesATargetThatIsNotTheWorkspacesOwn(t *testing.T) {
	setup := func(t *testing.T, build func(root, ws string) string) (ws, victim string) {
		t.Helper()
		root := t.TempDir()
		ws = filepath.Join(root, "ws")
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
		victim = build(root, ws)
		return ws, victim
	}
	cases := map[string]func(root, ws string) string{
		"absolute outside dir that is a git dir": func(root, ws string) string {
			v := mkGitDir(t, filepath.Join(root, "elsewhere"))
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+v+"\n")
			return v
		},
		"traversal": func(root, ws string) string {
			v := mkGitDir(t, filepath.Join(root, "elsewhere"))
			rel, err := filepath.Rel(ws, v)
			if err != nil || !strings.HasPrefix(rel, "..") {
				t.Fatalf("not a traversal: %q %v", rel, err)
			}
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+filepath.ToSlash(rel)+"\n")
			return v
		},
		"plain directory without HEAD": func(root, ws string) string {
			v := filepath.Join(root, "dir")
			if err := os.MkdirAll(filepath.Join(v, "info"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+v+"\n")
			return v
		},
		"worktree whose back-link names another workspace": func(root, ws string) string {
			common := mkGitDir(t, filepath.Join(root, "store"))
			wt := mkGitDir(t, filepath.Join(common, "worktrees", "wt"))
			writeTestFile(t, filepath.Join(wt, "commondir"), "../..\n")
			writeTestFile(t, filepath.Join(wt, "gitdir"), filepath.Join(root, "other", ".git")+"\n")
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+wt+"\n")
			return common
		},
		"worktree whose commondir is not a git dir": func(root, ws string) string {
			wt := mkGitDir(t, filepath.Join(root, "store", "worktrees", "wt"))
			victim := filepath.Join(root, "victim")
			if err := os.MkdirAll(filepath.Join(victim, "worktrees"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(wt, "commondir"), victim+"\n")
			writeTestFile(t, filepath.Join(wt, "gitdir"), filepath.Join(ws, ".git")+"\n")
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+wt+"\n")
			return victim
		},
		"worktree whose commondir is a git dir that does not own it": func(root, ws string) string {
			wt := mkGitDir(t, filepath.Join(root, "store", "worktrees", "wt"))
			other := mkGitDir(t, filepath.Join(root, "other"))
			writeTestFile(t, filepath.Join(wt, "commondir"), other+"\n")
			writeTestFile(t, filepath.Join(wt, "gitdir"), filepath.Join(ws, ".git")+"\n")
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+wt+"\n")
			return other
		},
		"submodule-looking dir under modules of a repository that does not contain the workspace": func(root, ws string) string {
			other := mkGitDir(t, filepath.Join(root, "unrelated", ".git"))
			sub := mkGitDir(t, filepath.Join(other, "modules", "sub"))
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+sub+"\n")
			return sub
		},
		"commondir that is a symlink": func(root, ws string) string {
			skipWithoutSymlinks(t)
			common := mkGitDir(t, filepath.Join(root, "store"))
			wt := mkGitDir(t, filepath.Join(common, "worktrees", "wt"))
			real := filepath.Join(root, "real-commondir")
			writeTestFile(t, real, "../..\n")
			if err := os.Symlink(real, filepath.Join(wt, "commondir")); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(wt, "gitdir"), filepath.Join(ws, ".git")+"\n")
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+wt+"\n")
			return common
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			ws, victim := setup(t, build)
			before := treeOf(t, victim)
			for _, err := range []error{
				EnsureWorkspaceExcluded(ws, []string{".mcp.json"}),
				RemoveWorkspaceExclusions(ws),
			} {
				if !errors.Is(err, ErrGitDirUnresolvable) {
					t.Fatalf("err = %v, want ErrGitDirUnresolvable", err)
				}
			}
			sameTree(t, "target", before, treeOf(t, victim))
		})
	}
}

// A `.git` file that is a symlink is never read.
func TestEnsureWorkspaceExcluded_GitFileThatIsASymlinkIsRefused(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	// Even a pointer to a perfectly good git directory.
	good := mkGitDir(t, filepath.Join(root, "good"))
	real := filepath.Join(root, "pointer")
	writeTestFile(t, real, "gitdir: "+good+"\n")
	if err := os.Symlink(real, filepath.Join(ws, ".git")); err != nil {
		t.Fatal(err)
	}
	before := treeOf(t, good)
	for _, err := range []error{
		EnsureWorkspaceExcluded(ws, []string{".mcp.json"}),
		RemoveWorkspaceExclusions(ws),
	} {
		if !errors.Is(err, ErrSymlinkDestination) || !ExcludeWarning(err) {
			t.Errorf("err = %v, want ErrSymlinkDestination (a warning, not a failure)", err)
		}
	}
	sameTree(t, "git dir", before, treeOf(t, good))
}

func TestIsNetworkPath(t *testing.T) {
	for p, want := range map[string]bool{
		`\\host\share\x`: true, "//host/share/x": true, `\\?\C:\x`: true,
		`C:\x\y`: false, "/home/user/x": false, "../x": false, "x": false, "": false,
	} {
		if got := isNetworkPath(p, true); got != want {
			t.Errorf("isNetworkPath(%q, windows) = %v, want %v", p, got, want)
		}
		if isNetworkPath(p, false) {
			t.Errorf("isNetworkPath(%q) off Windows = true, want false: //host is an ordinary path there", p)
		}
	}
}

// The guard still holds in the resolved git directory: a symlinked info/ there
// is refused like in a plain repository (D148).
func TestEnsureWorkspaceExcluded_LinkedWorktreeRefusesASymlinkedInfo(t *testing.T) {
	skipWithoutSymlinks(t)
	repo := commitedRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", "-b", "side", wt)
	elsewhere := t.TempDir()
	info := filepath.Join(repo, ".git", "info")
	if err := os.RemoveAll(info); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, info); err != nil {
		t.Fatal(err)
	}
	err := EnsureWorkspaceExcluded(wt, []string{".mcp.json"})
	if !errors.Is(err, ErrSymlinkDestination) || !ExcludeWarning(err) {
		t.Errorf("err = %v, want ErrSymlinkDestination", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("the write followed the link: %v", entries)
	}
}

// The main checkout and every linked worktree share one info/exclude, so each
// owns a block of its own: ensuring or removing one never touches another's,
// whatever the order (git check-ignore is the oracle).
func TestEnsureWorkspaceExcluded_WorktreesAndMainKeepEachOthersBlocks(t *testing.T) {
	repo := commitedRepo(t)
	wtA := filepath.Join(t.TempDir(), "a")
	wtB := filepath.Join(t.TempDir(), "b")
	git(t, repo, "worktree", "add", "-q", "-b", "side-a", wtA)
	git(t, repo, "worktree", "add", "-q", "-b", "side-b", wtB)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte("*.swp\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	owners := map[string]struct{ ws, path string }{
		"main": {repo, ".main-dir"},
		"a":    {wtA, ".a-dir"},
		"b":    {wtB, ".b-dir"},
	}
	check := func(step string, present map[string]bool) {
		t.Helper()
		for name, o := range owners {
			if got := gitIgnores(t, repo, o.path+"/f"); got != present[name] {
				t.Errorf("%s: %s's path ignored = %v, want %v\n%s", step, name, got, present[name], readFile(t, exclude))
			}
		}
		if !strings.HasPrefix(readFile(t, exclude), "*.swp\n") {
			t.Errorf("%s: the user's line was lost:\n%s", step, readFile(t, exclude))
		}
	}
	orders := [][]string{
		{"main", "a", "b"}, {"b", "a", "main"}, {"a", "main", "b"},
		{"a", "b", "main"}, {"b", "main", "a"}, {"main", "b", "a"},
	}
	for _, order := range orders {
		for _, reverse := range []bool{false, true} {
			present := map[string]bool{}
			for _, name := range order {
				o := owners[name]
				// Twice: ensuring again is idempotent and never disturbs the others.
				for i := 0; i < 2; i++ {
					if err := EnsureWorkspaceExcluded(o.ws, []string{o.path}); err != nil {
						t.Fatal(err)
					}
					present[name] = true
					check("ensure "+name, present)
				}
			}
			for i := range order {
				name := order[i]
				if reverse {
					name = order[len(order)-1-i]
				}
				if err := RemoveWorkspaceExclusions(owners[name].ws); err != nil {
					t.Fatal(err)
				}
				present[name] = false
				check("remove "+name, present)
			}
			if got := readFile(t, exclude); got != "*.swp\n" {
				t.Errorf("after %v (reverse=%v) the file is %q, want the user's line only", order, reverse, got)
			}
		}
	}
}

// A worktree whose own paths change rewrites its block and leaves the others.
func TestEnsureWorkspaceExcluded_AWorktreeReplacesOnlyItsOwnPaths(t *testing.T) {
	repo := commitedRepo(t)
	wtA := filepath.Join(t.TempDir(), "a")
	wtB := filepath.Join(t.TempDir(), "b")
	git(t, repo, "worktree", "add", "-q", "-b", "side-a", wtA)
	git(t, repo, "worktree", "add", "-q", "-b", "side-b", wtB)
	for ws, p := range map[string]string{wtA: ".a-old", wtB: ".b-dir"} {
		if err := EnsureWorkspaceExcluded(ws, []string{p}); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureWorkspaceExcluded(wtA, []string{".a-new"}); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{".a-old/f": false, ".a-new/f": true, ".b-dir/f": true} {
		if got := gitIgnores(t, repo, p); got != want {
			t.Errorf("%s ignored = %v, want %v", p, got, want)
		}
	}
}

// The strip of one block leaves the blocks of other keys byte-identical.
func TestStripExcludeBlock_OnlyItsOwnKey(t *testing.T) {
	ba, ea := blockMarkers("a")
	bb, eb := blockMarkers("b")
	content := "mine\n" + excludeMarkerBegin + "\n/m\n" + excludeMarkerEnd + "\n" + ba + "\n/a\n" + ea + "\n" + bb + "\n/b\n" + eb + "\n"
	if got, want := stripExcludeBlock(content, "a"), "mine\n"+excludeMarkerBegin+"\n/m\n"+excludeMarkerEnd+"\n"+bb+"\n/b\n"+eb+"\n"; got != want {
		t.Errorf("strip a = %q, want %q", got, want)
	}
	if got, want := stripExcludeBlock(content, ""), "mine\n"+ba+"\n/a\n"+ea+"\n"+bb+"\n/b\n"+eb+"\n"; got != want {
		t.Errorf("strip main = %q, want %q", got, want)
	}
}

// A submodule's git directory is trusted only when it is the one git keeps for
// this workspace: resolved, inside the resolved modules/ of an enclosing
// repository, and with core.worktree naming this workspace.
func TestEnsureWorkspaceExcluded_SubmoduleTargetMustBeTheWorkspacesOwn(t *testing.T) {
	cases := map[string]func(root, ws, parentGit string) string{
		"symlinked modules entry": func(root, ws, parentGit string) string {
			skipWithoutSymlinks(t)
			// A git directory elsewhere that even claims this workspace.
			other := mkGitDir(t, filepath.Join(root, "elsewhere"))
			writeTestFile(t, filepath.Join(other, "config"), "[core]\n\tworktree = "+filepath.ToSlash(ws)+"\n")
			if err := os.MkdirAll(filepath.Join(parentGit, "modules"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(parentGit, "modules", "m")); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+filepath.Join(parentGit, "modules", "m")+"\n")
			return other
		},
		"sibling submodule git directory": func(root, ws, parentGit string) string {
			sibling := mkGitDir(t, filepath.Join(parentGit, "modules", "sibling"))
			writeTestFile(t, filepath.Join(sibling, "config"), "[core]\n\tworktree = ../../../sibling\n")
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+sibling+"\n")
			return sibling
		},
		"submodule git directory without core.worktree": func(root, ws, parentGit string) string {
			own := mkGitDir(t, filepath.Join(parentGit, "modules", "sub"))
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+own+"\n")
			return own
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			parentGit := mkGitDir(t, filepath.Join(root, "parent", ".git"))
			ws := filepath.Join(root, "parent", "sub")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			victim := build(root, ws, parentGit)
			before := treeOf(t, victim)
			for _, err := range []error{
				EnsureWorkspaceExcluded(ws, []string{".mcp.json"}),
				RemoveWorkspaceExclusions(ws),
			} {
				if !errors.Is(err, ErrGitDirUnresolvable) {
					t.Fatalf("err = %v, want ErrGitDirUnresolvable", err)
				}
			}
			sameTree(t, "target", before, treeOf(t, victim))
		})
	}
}

// `git init --separate-git-dir` leaves a `.git` file whose target nothing ties to
// the workspace (no back-link, no commondir, no modules/): it is a documented
// limit, a warning and no exclude block, never an error that fails the sync.
func TestEnsureWorkspaceExcluded_SeparateGitDirIsAWarning(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "ws")
	store := filepath.Join(t.TempDir(), "store.git")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, ws, "init", "-q", "--separate-git-dir", store)
	if st, err := os.Stat(filepath.Join(ws, ".git")); err != nil || !st.Mode().IsRegular() {
		t.Fatalf(".git is not a file: %v", err)
	}
	before := treeOf(t, store)
	for _, err := range []error{
		EnsureWorkspaceExcluded(ws, []string{".mcp.json"}),
		RemoveWorkspaceExclusions(ws),
	} {
		if !errors.Is(err, ErrGitDirUnresolvable) || !ExcludeWarning(err) {
			t.Errorf("err = %v, want the ErrGitDirUnresolvable warning", err)
		}
	}
	sameTree(t, "separate git dir", before, treeOf(t, store))
}

// `git worktree remove` does not run anything of ours: the next Ensure or Remove,
// from the main checkout or any worktree, drops the blocks of worktrees that are
// gone and keeps the live ones.
func TestEnsureWorkspaceExcluded_BlocksOfRemovedWorktreesAreDropped(t *testing.T) {
	for _, via := range []string{"ensure from main", "remove from a sibling", "ensure from a sibling"} {
		t.Run(via, func(t *testing.T) {
			repo := commitedRepo(t)
			wtA := filepath.Join(t.TempDir(), "a")
			wtB := filepath.Join(t.TempDir(), "b")
			git(t, repo, "worktree", "add", "-q", "-b", "side-a", wtA)
			git(t, repo, "worktree", "add", "-q", "-b", "side-b", wtB)
			for ws, p := range map[string]string{wtA: ".a-dir", wtB: ".b-dir", repo: ".main-dir"} {
				if err := EnsureWorkspaceExcluded(ws, []string{p}); err != nil {
					t.Fatal(err)
				}
			}
			git(t, repo, "worktree", "remove", "--force", wtA)

			var err error
			switch via {
			case "ensure from main":
				err = EnsureWorkspaceExcluded(repo, []string{".main-dir"})
			case "remove from a sibling":
				err = RemoveWorkspaceExclusions(wtB)
			default:
				err = EnsureWorkspaceExcluded(wtB, []string{".b-dir"})
			}
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{".a-dir/f": false, ".b-dir/f": via != "remove from a sibling", ".main-dir/f": true}
			for p, w := range want {
				if got := gitIgnores(t, repo, p); got != w {
					t.Errorf("%s ignored = %v, want %v\n%s", p, got, w, readFile(t, filepath.Join(repo, ".git", "info", "exclude")))
				}
			}
		})
	}
}
