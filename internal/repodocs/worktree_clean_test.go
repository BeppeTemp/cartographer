package repodocs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// make worktree-clean deletes a branch, a worktree and a branch on a fork:
// what it must never do is as much a contract as what it does. A merged PR
// is the proof that work landed (a squash merge leaves the branch "unmerged"
// for git), so each case here is a way for that proof to be missing.
func TestWorktreeCleanRemovesOnlyWhatAMergedPRCovers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script, run by the Unix legs")
	}
	for _, bin := range []string{"bash", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not found", bin)
		}
	}
	root := repoRoot(t)
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// git and the stubbed gh run in a clean environment: the host's own git
	// configuration must not decide whether a bare repository may be read.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	prs := filepath.Join(tmp, "prs.tsv")
	bin := filepath.Join(tmp, "bin")
	env = append(env,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=user", "GIT_AUTHOR_EMAIL=user@example.com",
		"GIT_COMMITTER_NAME=user", "GIT_COMMITTER_EMAIL=user@example.com",
		"FAKE_GH_PRS="+prs,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	run := func(dir string, name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}

	// gh prints, for `pr list --head <branch>`, the lines of FAKE_GH_PRS for that branch.
	write(filepath.Join(bin, "gh"), `#!/bin/sh
for a; do [ "$p" = --head ] && b=$a; p=$a; done
awk -F'\t' -v b="$b" '$1 == b { print $2 "\t" $3 "\t" $4 }' "$FAKE_GH_PRS"
`, 0o755)

	up := filepath.Join(tmp, "up", "proj.git")
	fork := filepath.Join(tmp, "me", "proj.git")
	work := filepath.Join(tmp, "work")
	run(tmp, "git", "init", "-q", "--bare", "-b", "main", up)
	run(tmp, "git", "init", "-q", "--bare", "-b", "main", fork)
	run(tmp, "git", "init", "-q", "-b", "main", work)
	run(work, "git", "remote", "add", "upstream", up)
	run(work, "git", "remote", "add", "fork", fork)
	for _, s := range []string{"worktree-clean.sh", "upstream-repo.sh"} {
		write(filepath.Join(work, "scripts", s), readFile(t, root, "scripts/"+s), 0o755)
	}
	write(filepath.Join(work, "README"), "x\n", 0o644)
	run(work, "git", "add", ".")
	run(work, "git", "commit", "-q", "-m", "init")
	run(work, "git", "push", "-q", "upstream", "main")

	branch := func(name, slug string) (path, tip string) {
		path = filepath.Join(work, ".worktrees", slug)
		run(work, "git", "worktree", "add", "-q", "-b", name, path, "main")
		write(filepath.Join(path, slug+".txt"), name+"\n", 0o644)
		run(path, "git", "add", ".")
		run(path, "git", "commit", "-q", "-m", name)
		run(path, "git", "push", "-q", "fork", name)
		return path, run(path, "git", "rev-parse", "HEAD")
	}
	merged := func(name, tip string) {
		f, err := os.OpenFile(prs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(name + "\t7\t" + tip + "\tme\n"); err != nil {
			t.Fatal(err)
		}
	}

	donePath, doneTip := branch("fix/1-done", "1-done")
	merged("fix/1-done", doneTip)

	dirtyPath, dirtyTip := branch("fix/2-dirty", "2-dirty")
	merged("fix/2-dirty", dirtyTip)
	write(filepath.Join(dirtyPath, "wip.txt"), "wip\n", 0o644)

	extraPath, extraTip := branch("fix/3-extra", "3-extra")
	merged("fix/3-extra", extraTip)
	write(filepath.Join(extraPath, "late.txt"), "after the merge\n", 0o644)
	run(extraPath, "git", "add", ".")
	run(extraPath, "git", "commit", "-q", "-m", "after the merge")

	nopr, _ := branch("fix/4-nopr", "4-nopr")

	movedPath, movedTip := branch("fix/5-moved", "5-moved")
	merged("fix/5-moved", movedTip)
	later := run(movedPath, "git", "commit-tree", "HEAD^{tree}", "-p", "HEAD", "-m", "pushed after the merge")
	run(movedPath, "git", "push", "-q", "fork", later+":refs/heads/fix/5-moved")

	clean := func(args ...string) (string, bool) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{filepath.Join(work, "scripts", "worktree-clean.sh")}, args...)...)
		cmd.Dir, cmd.Env = work, env
		out, err := cmd.CombinedOutput()
		return string(out), err == nil
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	hasBranch := func(b string) bool { return run(work, "git", "branch", "--list", b) != "" }
	forkTip := func(b string) string {
		out := run(work, "git", "ls-remote", "--heads", "fork", "refs/heads/"+b)
		if out == "" {
			return ""
		}
		return strings.Fields(out)[0]
	}

	out, ok := clean("--dry-run")
	if !ok || !strings.Contains(out, "would  remove fix/1-done") {
		t.Fatalf("--dry-run: ok=%v\n%s", ok, out)
	}
	if !exists(donePath) || !hasBranch("fix/1-done") || forkTip("fix/1-done") != doneTip {
		t.Fatalf("--dry-run removed something\n%s", out)
	}

	out, ok = clean()
	if !ok {
		t.Fatalf("worktree-clean failed\n%s", out)
	}
	if exists(donePath) || hasBranch("fix/1-done") || forkTip("fix/1-done") != "" {
		t.Errorf("a merged PR's worktree, local branch and fork branch must all go\n%s", out)
	}
	if !strings.Contains(out, "verified gone") {
		t.Errorf("the fork deletion must be read back from the remote\n%s", out)
	}
	for _, c := range []struct {
		why, name, path, remote string
	}{
		{"uncommitted changes", "fix/2-dirty", dirtyPath, dirtyTip},
		{"a commit the merged PR does not have", "fix/3-extra", extraPath, extraTip},
		{"no merged PR", "fix/4-nopr", nopr, ""},
	} {
		if !exists(c.path) || !hasBranch(c.name) {
			t.Errorf("%s (%s) must be kept\n%s", c.name, c.why, out)
		}
		if c.remote != "" && forkTip(c.name) != c.remote {
			t.Errorf("%s: the fork branch must be kept (%s)\n%s", c.name, c.why, out)
		}
	}
	// Merged and clean locally, but the fork branch moved after the merge:
	// the local side goes, the fork branch stays.
	if exists(movedPath) || hasBranch("fix/5-moved") {
		t.Errorf("fix/5-moved: its local worktree and branch are covered by the merged PR\n%s", out)
	}
	if forkTip("fix/5-moved") != later {
		t.Errorf("fix/5-moved: the fork branch moved after the merge and must be kept\n%s", out)
	}
}
