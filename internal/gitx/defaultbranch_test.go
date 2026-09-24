package gitx

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// emptyMasterRemote creates an empty bare repository whose HEAD is "master",
// as `git init --bare` does on a host whose init.defaultBranch is "master".
func emptyMasterRemote(t *testing.T) (base, bare string) {
	t.Helper()
	base = t.TempDir()
	bare = filepath.Join(base, "remote.git")
	mustGit(t, base, "-c", "init.defaultBranch=master", "init", "-q", "--bare", bare)
	return base, bare
}

// The clone of an empty remote has an unborn HEAD on the host's default
// branch name; Init pins it to main so it matches `kb create` (D264).
func TestInit_UnbornCloneIsPinnedToMain(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	base, bare := emptyMasterRemote(t)
	clone := filepath.Join(base, "clone")
	mustGit(t, base, "-c", "init.defaultBranch=master", "clone", "-q", bare, clone)
	if b, _ := Branch(clone); b != "master" {
		t.Fatalf("fixture: clone of the empty remote is on %q, want master", b)
	}
	if !HeadUnborn(clone) {
		t.Fatal("HeadUnborn = false for the clone of an empty remote")
	}
	if err := Init(clone); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if b, _ := Branch(clone); b != DefaultBranch {
		t.Fatalf("branch after Init = %q, want %q", b, DefaultBranch)
	}
}

func TestInit_RepoWithCommitsKeepsItsBranch(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	mustGit(t, dir, "-c", "init.defaultBranch=master", "init", "-q")
	mustGit(t, dir, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "--allow-empty", "-m", "seed")
	if HeadUnborn(dir) {
		t.Fatal("HeadUnborn = true for a repository with a commit")
	}
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if b, _ := Branch(dir); b != "master" {
		t.Fatalf("branch after Init = %q, want master kept", b)
	}
}

func TestLsRemote_EmptyDanglingAndDefault(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	base, bare := emptyMasterRemote(t)
	clone := filepath.Join(base, "clone")
	mustGit(t, base, "clone", "-q", bare, clone)

	refs, err := LsRemote(clone, "origin")
	if err != nil {
		t.Fatalf("LsRemote empty: %v", err)
	}
	if refs.HasRefs || refs.Default != "" || len(refs.Branches) != 0 {
		t.Fatalf("empty remote refs = %+v", refs)
	}

	// Push main while the remote's HEAD still names master: HEAD dangles.
	mustGit(t, clone, "checkout", "-q", "-b", "main")
	mustGit(t, clone, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "--allow-empty", "-m", "seed")
	mustGit(t, clone, "push", "-q", "origin", "main")
	refs, err = LsRemote(clone, "origin")
	if err != nil {
		t.Fatalf("LsRemote dangling: %v", err)
	}
	if !refs.HasRefs || refs.Default != "" || !refs.HasBranch("main") {
		t.Fatalf("dangling-HEAD refs = %+v", refs)
	}

	mustGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	mustGit(t, clone, "fetch", "-q", "origin")
	refs, err = LsRemote(clone, "origin")
	if err != nil {
		t.Fatalf("LsRemote: %v", err)
	}
	if refs.Default != "main" {
		t.Fatalf("default = %q, want main (refs %+v)", refs.Default, refs)
	}
	// origin/HEAD is refreshed from the remote, not left as clone wrote it.
	if got := mustGit(t, clone, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); got != "origin/main" {
		t.Fatalf("origin/HEAD = %q, want origin/main", got)
	}
}

func TestLsRemote_UnreachableRemoteIsAnError(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	mustGit(t, dir, "remote", "add", "origin", filepath.Join(dir, "missing.git"))
	if _, err := LsRemote(dir, "origin"); err == nil {
		t.Fatal("LsRemote on a missing remote returned no error")
	}
}

func TestParseLsRemoteSymref(t *testing.T) {
	out := "ref: refs/heads/trunk\tHEAD\n" +
		"1111111111111111111111111111111111111111\tHEAD\n" +
		"1111111111111111111111111111111111111111\trefs/heads/trunk\n" +
		"2222222222222222222222222222222222222222\trefs/heads/feature\n" +
		"3333333333333333333333333333333333333333\trefs/tags/v1\n"
	got := parseLsRemoteSymref(out)
	want := RemoteRefs{HasRefs: true, Default: "trunk", Branches: []string{"trunk", "feature"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parse = %+v, want %+v", got, want)
	}
	if got := parseLsRemoteSymref(""); got.HasRefs {
		t.Fatalf("empty output parsed as %+v", got)
	}
	// A HEAD naming a branch the remote lacks is no default.
	if got := parseLsRemoteSymref("ref: refs/heads/master\tHEAD\n2222222222222222222222222222222222222222\trefs/heads/main\n"); got.Default != "" {
		t.Fatalf("dangling HEAD default = %q", got.Default)
	}
}
