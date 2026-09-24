package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateGitIdentity leaves git with no identity to find: HOME and
// XDG_CONFIG_HOME point at an empty directory, the system config is skipped,
// and the identity environment variables are unset. It returns that empty
// home so a test can add a global config to it.
func isolateGitIdentity(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(k, "") // registers the restore
		os.Unsetenv(k)
	}
	return home
}

// TestCommit_NoGitIdentity_AuthorBecomesCommitter reproduces #410: with no
// user.email anywhere, --author alone fails with "Committer identity
// unknown". Commit must supply the author as committer instead.
func TestCommit_NoGitIdentity_AuthorBecomesCommitter(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	isolateGitIdentity(t)
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Forbid guessing user@host, which succeeds on a host with a resolvable
	// domain and would hide the missing identity.
	if out, err := runGit(dir, "config", "user.useConfigOnly", "true"); err != nil {
		t.Fatalf("git config: %v: %s", err, out)
	}
	if _, err := runGit(dir, "var", "GIT_COMMITTER_IDENT"); err == nil {
		t.Fatal("precondition: git resolved a committer identity in an isolated environment")
	}
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("# Index\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "init", "Author Person", "author@example.com"); err != nil {
		t.Fatalf("Commit with an author and no git identity: %v", err)
	}
	out, err := runGit(dir, "log", "-1", "--format=%an <%ae> %cn <%ce>")
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	want := "Author Person <author@example.com> Author Person <author@example.com>"
	if got := strings.TrimSpace(out); got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}
}

// TestCommit_ConfiguredCommitterIsKept verifies the fallback only fills a
// gap: a committer git can resolve (here from the repo config) is recorded
// unchanged even though an author is supplied.
func TestCommit_ConfiguredCommitterIsKept(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	isolateGitIdentity(t)
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	runGit(dir, "config", "user.name", "Config User")
	runGit(dir, "config", "user.email", "config@example.com")
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("# Index\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "init", "Author Person", "author@example.com"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	out, _ := runGit(dir, "log", "-1", "--format=%an <%ae> %cn <%ce>")
	want := "Author Person <author@example.com> Config User <config@example.com>"
	if got := strings.TrimSpace(out); got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}
}
