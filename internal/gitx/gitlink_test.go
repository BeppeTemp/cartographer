package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// kbRepo is a repository with one commit, the identity set locally so that
// Commit and the raw git calls of a test need nothing from the host.
func kbRepo(t *testing.T) string {
	t.Helper()
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	runIn(t, dir, "config", "user.email", "test@wiki.local")
	runIn(t, dir, "config", "user.name", "Wiki Test")
	writeFile(t, dir, "index.md", "# Index\n")
	if err := Commit(dir, "init", "", ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// nestedRepo makes dir/rel a repository of its own, with one commit, and arms
// its core.fsmonitor to write the file it returns: the file exists only if git
// ran the nested repository's configuration. The command is a POSIX shell
// line, so on Windows the repository is made but not armed, and the checks on
// the index below are the ones that bite there.
func nestedRepo(t *testing.T, dir, rel string) (marker string) {
	t.Helper()
	nested := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	runIn(t, nested, "init", "-q")
	writeFile(t, nested, "a.txt", "nested\n")
	runIn(t, nested, "add", "-A")
	runIn(t, nested, "commit", "-qm", "nested")
	marker = filepath.Join(t.TempDir(), "fsmonitor-ran")
	if runtime.GOOS != "windows" {
		runIn(t, nested, "config", "core.fsmonitor", "echo ran > '"+filepath.ToSlash(marker)+"'")
	}
	return marker
}

func ran(marker string) bool {
	_, err := os.Stat(marker)
	return err == nil
}

// gitlinks is what "git ls-files -s" says is recorded as a gitlink, in the
// index (staged) or in HEAD.
func gitlinks(t *testing.T, dir string, inHEAD bool) []string {
	t.Helper()
	args := []string{"ls-files", "-s"}
	if inHEAD {
		args = []string{"ls-tree", "-r", "HEAD"}
	}
	var out []string
	for _, line := range strings.Split(runIn(t, dir, args...), "\n") {
		if strings.HasPrefix(line, "160000") {
			out = append(out, line)
		}
	}
	return out
}

func head(t *testing.T, dir string) string {
	t.Helper()
	sha, err := HeadSHA(dir)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// commitAndRecord puts the nested repository at rel into HEAD as a gitlink
// with plain git, the state a KB is in when a gitlink was recorded by an
// earlier version.
func commitAndRecord(t *testing.T, dir, rel string) {
	t.Helper()
	runIn(t, dir, "-c", "core.fsmonitor=false", "add", rel)
	runIn(t, dir, "-c", "core.fsmonitor=false", "commit", "-qm", "record "+rel)
	if len(gitlinks(t, dir, true)) != 1 {
		t.Fatalf("setup: want one gitlink in HEAD, got %v", gitlinks(t, dir, true))
	}
}

// Every git command of the server carries the configuration that keeps it out
// of a nested repository, after whatever the caller's environment holds.
func TestGitEnv_CarriesTheServerConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_PARAMETERS", "'user.name'='Prior'")
	v, ok := envValue(gitEnv([]string{"GIT_CONFIG_PARAMETERS='diff.ignoreSubmodules=none'"}), "GIT_CONFIG_PARAMETERS")
	if !ok {
		t.Fatal("GIT_CONFIG_PARAMETERS is not set")
	}
	for _, want := range []string{"'diff.ignoreSubmodules=all'", "'submodule.recurse=false'", "'fetch.recurseSubmodules=false'", "'core.fsmonitor=false'"} {
		if !strings.Contains(v, want) {
			t.Errorf("GIT_CONFIG_PARAMETERS = %q, missing %s", v, want)
		}
	}
	if !strings.Contains(v, "'diff.ignoreSubmodules=none'") || strings.Index(v, "'diff.ignoreSubmodules=all'") < strings.Index(v, "'diff.ignoreSubmodules=none'") {
		t.Errorf("GIT_CONFIG_PARAMETERS = %q: ours must come after the caller's, so that ours win", v)
	}

	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	runIn(t, dir, "init", "-q")
	runIn(t, dir, "config", "diff.ignoreSubmodules", "none") // the repository asks for the opposite
	out, err := runGit(dir, "config", "--get", "diff.ignoreSubmodules")
	if err != nil || strings.TrimSpace(out) != "all" {
		t.Errorf("the server's git sees diff.ignoreSubmodules = %q (%v), want all", out, err)
	}
}

// A gitlink the history already holds, with a nested repository that is armed:
// no operation the server runs may execute that repository's configuration.
func TestServerGit_NeverRunsANestedRepositoryConfig(t *testing.T) {
	dir := kbRepo(t)
	marker := nestedRepo(t, dir, "skills/nested")
	commitAndRecord(t, dir, "skills/nested")

	steps := []struct {
		name string
		run  func() error
	}{
		{"Status", func() error { _, err := Status(dir); return err }},
		{"StatusPaths", func() error { _, err := StatusPaths(dir, "skills"); return err }},
		{"ListFiles", func() error { _, err := ListFiles(dir); return err }},
		{"StagedChanges", func() error {
			writeFile(t, dir, "one.md", "one\n")
			_, err := StagedChanges(dir)
			return err
		}},
		{"DiscardChanges", func() error { return DiscardChanges(dir) }},
		{"Commit", func() error {
			writeFile(t, dir, "two.md", "two\n")
			return Commit(dir, "two", "", "")
		}},
		{"StashPushAll", func() error {
			writeFile(t, dir, "three.md", "three\n")
			return StashPushAllEnv(dir)
		}},
		{"StashPop", func() error { return StashPop(dir) }},
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if ran(marker) {
			t.Fatalf("%s ran the nested repository's core.fsmonitor", s.name)
		}
	}
	// The server committed its own change and left the recorded gitlink as it was.
	if got := runIn(t, dir, "ls-tree", "HEAD", "two.md"); got == "" {
		t.Error("the commit of a regular file did not land")
	}
	if len(gitlinks(t, dir, true)) != 1 {
		t.Errorf("HEAD gitlinks = %v, want the one recorded before", gitlinks(t, dir, true))
	}
}

// A nested repository new to the index is never recorded: the commit is
// refused whole, the index keeps no trace of it, and the config is not run.
func TestCommit_RefusesANestedRepositoryAndStagesNothingOfIt(t *testing.T) {
	dir := kbRepo(t)
	before := head(t, dir)
	marker := nestedRepo(t, dir, "skills/nested")
	writeFile(t, dir, "note.md", "a regular change\n")

	err := Commit(dir, "write", "", "")
	var gl *GitlinkError
	if !errors.Is(err, ErrGitlink) || !errors.As(err, &gl) || len(gl.Paths) != 1 || gl.Paths[0] != "skills/nested" {
		t.Fatalf("Commit = %v, want a *GitlinkError for skills/nested", err)
	}
	if head(t, dir) != before {
		t.Error("a refused commit moved HEAD")
	}
	if g := gitlinks(t, dir, false); len(g) != 0 {
		t.Errorf("the index still holds a gitlink: %v", g)
	}
	if ran(marker) {
		t.Error("the nested repository's core.fsmonitor ran")
	}
	// Status and the next commit attempt stay quiet too, and refuse again.
	if _, err := Status(dir); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "write again", "", ""); !errors.Is(err, ErrGitlink) {
		t.Errorf("second Commit = %v, want ErrGitlink", err)
	}
	if ran(marker) {
		t.Error("the nested repository's core.fsmonitor ran")
	}

	// Once the nested repository is gone the same tree commits.
	if err := os.RemoveAll(filepath.Join(dir, "skills")); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "write", "", ""); err != nil {
		t.Fatalf("Commit after removing it: %v", err)
	}
	if got := runIn(t, dir, "ls-tree", "HEAD", "note.md"); got == "" {
		t.Error("the regular change did not land")
	}
}

// The first commit of a repository is held to the same rule.
func TestCommit_UnbornHeadRefusesANestedRepository(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	runIn(t, dir, "config", "user.email", "test@wiki.local")
	runIn(t, dir, "config", "user.name", "Wiki Test")
	nestedRepo(t, dir, "skills/nested")
	writeFile(t, dir, "index.md", "# Index\n")
	if err := Commit(dir, "init", "", ""); !errors.Is(err, ErrGitlink) {
		t.Fatalf("Commit = %v, want ErrGitlink", err)
	}
	if !HeadUnborn(dir) {
		t.Error("a refused first commit created HEAD")
	}
}

// A gitlink put in the index by another means than Commit's own staging is
// refused by every commit path, and the real index is left clean of it.
func TestCommit_RefusesAGitlinkStagedByOtherMeans(t *testing.T) {
	const fake = "1111111111111111111111111111111111111111"

	t.Run("commit after update-index", func(t *testing.T) {
		dir := kbRepo(t)
		before := head(t, dir)
		runIn(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+fake+",skills/linked")
		if err := commit(dir, "write", "", ""); !errors.Is(err, ErrGitlink) {
			t.Fatalf("commit = %v, want ErrGitlink", err)
		}
		if head(t, dir) != before {
			t.Error("HEAD moved")
		}
	})

	t.Run("Commit with a nested repository staged by hand", func(t *testing.T) {
		dir := kbRepo(t)
		before := head(t, dir)
		marker := nestedRepo(t, dir, "skills/nested")
		writeFile(t, dir, "note.md", "a regular change\n")
		runIn(t, dir, "-c", "core.fsmonitor=false", "add", "skills/nested")
		if len(gitlinks(t, dir, false)) != 1 {
			t.Fatalf("setup: the gitlink is not staged: %v", gitlinks(t, dir, false))
		}
		if err := Commit(dir, "write", "", ""); !errors.Is(err, ErrGitlink) {
			t.Fatalf("Commit = %v, want ErrGitlink", err)
		}
		if head(t, dir) != before {
			t.Error("HEAD moved")
		}
		if g := gitlinks(t, dir, false); len(g) != 0 {
			t.Errorf("the index still holds the gitlink: %v", g)
		}
		if ran(marker) {
			t.Error("the nested repository's core.fsmonitor ran")
		}
	})

	t.Run("Commit with a staged gitlink whose folder is gone", func(t *testing.T) {
		dir := kbRepo(t)
		writeFile(t, dir, "note.md", "a regular change\n")
		runIn(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+fake+",skills/linked")
		// "git add -A" sees the entry's folder is not there and stages the
		// removal: the commit lands without the gitlink.
		if err := Commit(dir, "write", "", ""); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if g := gitlinks(t, dir, true); len(g) != 0 {
			t.Errorf("HEAD holds a gitlink: %v", g)
		}
	})

	t.Run("CommitPaths of a nested repository", func(t *testing.T) {
		dir := kbRepo(t)
		before := head(t, dir)
		marker := nestedRepo(t, dir, "skills/nested")
		if err := CommitPaths(dir, []string{"skills/nested"}, "write", "", ""); !errors.Is(err, ErrGitlink) {
			t.Fatalf("CommitPaths = %v, want ErrGitlink", err)
		}
		if head(t, dir) != before {
			t.Error("HEAD moved")
		}
		if ran(marker) {
			t.Error("the nested repository's core.fsmonitor ran")
		}
	})
}

// A gitlink the history holds stays out of "git add -A" while its directory
// still carries a .git (that add is the one command that looks inside); the
// server records no change to it, and its removal is recorded as any other.
func TestCommit_LeavesARecordedGitlinkAlone(t *testing.T) {
	dir := kbRepo(t)
	marker := nestedRepo(t, dir, "skills/nested")
	commitAndRecord(t, dir, "skills/nested")
	recorded := gitlinks(t, dir, true)[0]

	// The nested repository moves on, and so does a regular file.
	nested := filepath.Join(dir, "skills", "nested")
	writeFile(t, nested, "b.txt", "more\n")
	runIn(t, nested, "-c", "core.fsmonitor=false", "add", "-A")
	runIn(t, nested, "-c", "core.fsmonitor=false", "commit", "-qm", "moved on")
	writeFile(t, dir, "note.md", "a regular change\n")
	if err := Commit(dir, "write", "", ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if ran(marker) {
		t.Fatal("the nested repository's core.fsmonitor ran")
	}
	if got := gitlinks(t, dir, true); len(got) != 1 || got[0] != recorded {
		t.Errorf("HEAD gitlinks = %v, want %v unchanged: the server records no change to one", got, recorded)
	}

	// The nested repository is removed: that is the removal of a gitlink, which
	// a commit may record.
	if err := os.RemoveAll(nested); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "remove it", "", ""); err != nil {
		t.Fatalf("Commit after removing the folder: %v", err)
	}
	if got := gitlinks(t, dir, true); len(got) != 0 {
		t.Errorf("HEAD gitlinks = %v, want none after the removal", got)
	}
}

// A merge that brings a gitlink from the other side is finished by Commit; a
// nested repository the working tree adds to that merge is still refused.
func TestCommit_AMergeMayBringTheOtherSidesGitlink(t *testing.T) {
	const fake = "2222222222222222222222222222222222222222"
	dir := kbRepo(t)
	main := runIn(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	runIn(t, dir, "checkout", "-q", "-b", "other")
	runIn(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+fake+",vendor/lib")
	runIn(t, dir, "commit", "-qm", "other records a gitlink")
	runIn(t, dir, "checkout", "-q", main)
	writeFile(t, dir, "mine.md", "mine\n")
	runIn(t, dir, "add", "-A")
	runIn(t, dir, "commit", "-qm", "mine")

	merge := func() {
		t.Helper()
		if _, err := MergeNoCommitNoFFEnv(dir, "other"); err != nil {
			t.Fatalf("merge: %v", err)
		}
	}
	merge()
	nestedRepo(t, dir, "skills/nested")
	if err := Commit(dir, "merge", "", ""); !errors.Is(err, ErrGitlink) {
		t.Fatalf("Commit in a merge with a new nested repository = %v, want ErrGitlink", err)
	}
	var gl *GitlinkError
	if err := Commit(dir, "merge", "", ""); !errors.As(err, &gl) || len(gl.Paths) != 1 || gl.Paths[0] != "skills/nested" {
		t.Fatalf("the refusal must name only the new one, not the other side's: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(dir, "skills")); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "merge", "", ""); err != nil {
		t.Fatalf("Commit of the merge: %v", err)
	}
	if got := gitlinks(t, dir, true); len(got) != 1 || !strings.Contains(got[0], "vendor/lib") {
		t.Errorf("the merge commit's gitlinks = %v, want the other side's vendor/lib", got)
	}
}
