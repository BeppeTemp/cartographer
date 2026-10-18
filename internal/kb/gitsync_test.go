package kb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// initGitKB creates a temp KB via Init and skips the test if the initial git
// commit did not happen (git unavailable or not configured on this machine).
func initGitKB(t *testing.T) (*KB, string) {
	t.Helper()
	dir := tempKB(t)
	k, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	sha, err := gitx.HeadSHA(dir)
	if err != nil {
		t.Skipf("git not available or initial commit failed: %v", err)
	}
	return k, sha
}

// TestCommitOp_GitRepo_DirtyTree verifies that CommitOp on a git repo with a
// dirty working tree always creates a new commit (#903 WP3: autocommit is no
// longer a switch — a git repo always commits).
func TestCommitOp_GitRepo_DirtyTree(t *testing.T) {
	t.Parallel()
	k, sha1 := initGitKB(t)

	if err := k.WriteFileAtomic("data/gitsync-note.md", []byte("---\ntype: Note\ntitle: GitSync\n---\ntest\n")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	if _, err := k.CommitOp("test: gitsync dirty tree"); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}

	sha2, _ := gitx.HeadSHA(k.Root)
	if sha1 == sha2 {
		t.Fatal("CommitOp: expected new commit but HEAD SHA is unchanged")
	}
}

// TestCommitOp_NotARepo_NoCommit verifies that CommitOp is a no-op when the KB
// root is not a git repository (#903 WP3: a repo is the only switch left).
func TestCommitOp_NotARepo_NoCommit(t *testing.T) {
	t.Parallel()
	dir := tempKB(t)
	k, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Drop the git repository: the KB skeleton stays, but it is no longer a
	// repo, so CommitOp must be a no-op (#903 WP3).
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("remove .git: %v", err)
	}
	if err := k.WriteFileAtomic("data/gitsync-note.md", []byte("---\ntype: Note\ntitle: GitSync\n---\ntest\n")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	sha, err := k.CommitOp("test: not a repo")
	if err != nil {
		t.Fatalf("CommitOp unexpected error: %v", err)
	}
	if sha != "" {
		t.Fatalf("CommitOp on a non-repo returned sha %q, want empty", sha)
	}
}

// TestCommitOp_PerKBIdentity verifies that CommitOp uses k.GitAuthorName/
// GitAuthorEmail as the commit author and picks up the committer from
// k.GitEnv (GIT_COMMITTER_NAME/EMAIL).
func TestCommitOp_PerKBIdentity(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	k.GitAuthorName = "Author Person"
	k.GitAuthorEmail = "author@wiki.local"
	k.GitAuthorExplicit = true
	k.GitEnv = []string{"GIT_COMMITTER_NAME=Committer Bot", "GIT_COMMITTER_EMAIL=committer@wiki.local"}

	if err := k.WriteFileAtomic("data/gitsync-identity.md", []byte("---\ntype: Note\ntitle: Identity\n---\ntest\n")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if _, err := k.CommitOp("test: per-KB identity"); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}

	out, err := gitx.HeadSHA(k.Root)
	if err != nil || out == "" {
		t.Fatalf("HeadSHA: %v", err)
	}
	log, logErr := gitLogFormat(t, k.Root, "%an <%ae> %cn <%ce>")
	if logErr != nil {
		t.Fatalf("git log: %v", logErr)
	}
	want := "Author Person <author@wiki.local> Committer Bot <committer@wiki.local>"
	if log != want {
		t.Fatalf("git log identity = %q, want %q", log, want)
	}
}

// TestCommitOp_DefaultIdentity_NoOverride verifies that CommitOp falls back
// to the package defaults (cartographer/cartographer@localhost) when
// GitAuthorName/GitAuthorEmail are unset, preserving the pre-M3 behaviour.
func TestCommitOp_DefaultIdentity_NoOverride(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	// Explicitly clear the fixture's repository identity and force the final
	// fallback, so the placeholder assertion is independent of user config.
	k.GitAuthorName, k.GitAuthorEmail = defaultGitAuthorName, defaultGitAuthorEmail
	k.GitAuthorExplicit = true

	if err := k.WriteFileAtomic("data/gitsync-default-identity.md", []byte("---\ntype: Note\ntitle: Default\n---\ntest\n")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if _, err := k.CommitOp("test: default identity"); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}

	log, err := gitLogFormat(t, k.Root, "%an <%ae>")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	want := "cartographer <cartographer@localhost>"
	if log != want {
		t.Fatalf("git log author = %q, want %q", log, want)
	}
}

func TestCommitOp_NativeRepositoryIdentity(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	gitHere(t, k.Root, "config", "user.name", "Repository User")
	gitHere(t, k.Root, "config", "user.email", "repo@example.test")
	if err := k.WriteFileAtomic("data/native.md", []byte("native\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("native"); err != nil {
		t.Fatal(err)
	}
	got, err := gitLogFormat(t, k.Root, "%an <%ae>")
	if err != nil || got != "Repository User <repo@example.test>" {
		t.Fatalf("native identity = %q, %v", got, err)
	}
}

func TestCommitOp_PlaceholderFallbackWithoutRepositoryIdentity(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	k.GitEnv = []string{
		"GIT_AUTHOR_NAME=",
		"GIT_AUTHOR_EMAIL=",
		"GIT_COMMITTER_NAME=Committer",
		"GIT_COMMITTER_EMAIL=committer@example.test",
	}
	if err := k.WriteFileAtomic("data/fallback.md", []byte("fallback\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("fallback"); err != nil {
		t.Fatal(err)
	}
	got, err := gitLogFormat(t, k.Root, "%an <%ae>")
	if err != nil || got != "cartographer <cartographer@localhost>" {
		t.Fatalf("placeholder identity = %q, %v", got, err)
	}
}

func TestGitStatusSnapshot_NoRemoteAndIdentityWarning(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	k.GitAuthorEmail = defaultGitAuthorEmail
	if s := k.GitStatusSnapshot(); s.State != "no_remote" || s.IdentityWarning {
		t.Fatalf("status = %+v", s)
	}
}

func TestShouldWarnGitIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, email string
		remote      bool
		want        bool
	}{
		{name: "placeholder with remote", email: defaultGitAuthorEmail, remote: true, want: true},
		{name: "configured identity", email: "bot@example.test", remote: true},
		{name: "no remote", email: defaultGitAuthorEmail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldWarnGitIdentity(tc.remote, tc.email); got != tc.want {
				t.Fatalf("ShouldWarnGitIdentity = %v, want %v", got, tc.want)
			}
		})
	}
}

// gitLogFormat runs "git log -1 --format=<format>" and returns the trimmed output.
func gitLogFormat(t *testing.T, dir, format string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "log", "-1", "--format="+format)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TestCommitOp_CleanTree_NoCommit verifies that CommitOp is a no-op when the
// working tree is already clean, even with AutoCommit=true.
func TestCommitOp_CleanTree_NoCommit(t *testing.T) {
	t.Parallel()
	k, sha1 := initGitKB(t)

	// Working tree is clean after Init — CommitOp should be a no-op.
	if _, err := k.CommitOp("test: gitsync clean tree"); err != nil {
		t.Fatalf("CommitOp unexpected error: %v", err)
	}

	sha2, _ := gitx.HeadSHA(k.Root)
	if sha1 != sha2 {
		t.Fatal("CommitOp: expected no commit on clean tree but HEAD SHA changed")
	}
}

func TestRedactRemoteURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"https with user and token", "https://user:ghp_secret@gitlab.com/o/wiki.git", "https://gitlab.com/o/wiki.git"},
		{"https with token only", "https://ghp_secret@github.com/o/wiki.git", "https://github.com/o/wiki.git"},
		{"https without credentials", "https://github.com/o/wiki.git", "https://github.com/o/wiki.git"},
		{"ssh keeps the username", "ssh://git@github.com/o/wiki.git", "ssh://git@github.com/o/wiki.git"},
		{"ssh drops the password", "ssh://git:secret@github.com/o/wiki.git", "ssh://git@github.com/o/wiki.git"},
		{"scp-style is left alone", "git@github.com:o/wiki.git", "git@github.com:o/wiki.git"},
		{"local path is left alone", "/srv/wiki.git", "/srv/wiki.git"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactRemoteURL(tc.in); got != tc.want {
				t.Errorf("RedactRemoteURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(RedactRemoteURL(tc.in), "secret") {
				t.Errorf("RedactRemoteURL(%q) leaked credential material", tc.in)
			}
		})
	}
}

func TestRemoteInfo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	k, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if url, ok := k.RemoteInfo(); ok || url != "" {
		t.Fatalf("RemoteInfo without origin = (%q, %v), want (\"\", false)", url, ok)
	}
	if err := gitx.AddRemote(k.Root, "origin", "https://user:ghp_secret@gitlab.com/o/wiki.git"); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	url, ok := k.RemoteInfo()
	if !ok || url != "https://gitlab.com/o/wiki.git" {
		t.Fatalf("RemoteInfo with origin = (%q, %v), want (%q, true)", url, ok, "https://gitlab.com/o/wiki.git")
	}
}
