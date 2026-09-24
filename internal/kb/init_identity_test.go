package kb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolateGitIdentity leaves git with no identity to find (see the gitx test
// of the same name) and returns the empty home, where a test may write a
// global config. user.useConfigOnly stops git from guessing user@host, which
// succeeds on a host with a resolvable domain.
func isolateGitIdentity(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	writeGlobalGitConfig(t, home, "[user]\n\tuseConfigOnly = true\n")
	return home
}

func writeGlobalGitConfig(t *testing.T, home, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInitWithIdentity_NoGitIdentity reproduces #410: on a machine with no
// git identity the initial commit used to fail silently, leaving an unborn
// branch for `kb create` to push.
func TestInitWithIdentity_NoGitIdentity(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	isolateGitIdentity(t)
	root := filepath.Join(t.TempDir(), "kb-a")
	if _, err := InitWithIdentity(root, "Author Person", "author@example.com"); err != nil {
		t.Fatalf("InitWithIdentity: %v", err)
	}
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%an <%ae> %cn <%ce>").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	want := "Author Person <author@example.com> Author Person <author@example.com>"
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}
}

// TestInit_InitialCommitErrorPropagates verifies a failed initial commit is
// reported instead of swallowed. Signing through a missing gpg program makes
// git commit fail portably, without hooks or the execute bit.
func TestInit_InitialCommitErrorPropagates(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	home := isolateGitIdentity(t)
	writeGlobalGitConfig(t, home, "[user]\n\tuseConfigOnly = true\n"+
		"[commit]\n\tgpgSign = true\n[gpg]\n\tprogram = cartographer-no-such-gpg\n")
	root := filepath.Join(t.TempDir(), "kb-a")
	_, err := InitWithIdentity(root, "Author Person", "author@example.com")
	if err == nil {
		t.Fatal("InitWithIdentity succeeded although the initial commit cannot be signed")
	}
	if !strings.Contains(err.Error(), "initial commit") {
		t.Fatalf("error = %v, want it to name the initial commit", err)
	}
}
