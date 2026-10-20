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

// TestCommitOp_DirtyTreeCommits verifies that CommitOp on a dirty working tree
// creates a new commit: every write is a commit, whatever the configuration
// (D903).
func TestCommitOp_DirtyTreeCommits(t *testing.T) {
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

// TestCommitOp_NotARepository_NoOp verifies that a KB that is not a git
// repository is the one case that does not commit, and is not an error.
func TestCommitOp_NotARepository_NoOp(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "index.md"), []byte("---\ntype: Index\ntitle: x\n---\n# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if sha, err := k.CommitOp("test: not a repo"); sha != "" || err != nil {
		t.Fatalf("CommitOp on a non-repository = (%q, %v), want a silent no-op", sha, err)
	}
}

// A commit with a declared author has it as author and committer (D950); one
// with none is by the KB's default identity — the server's when none is
// configured, the configured one otherwise — whatever git would have
// resolved from the repository, the user config or the process environment.
func TestCommitOp_WriterIsAuthorAndCommitter(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	gitHere(t, k.Root, "config", "user.name", "Repository User")
	gitHere(t, k.Root, "config", "user.email", "repo@example.test")
	k.GitEnv = []string{"GIT_COMMITTER_NAME=Ignored", "GIT_COMMITTER_EMAIL=ignored@example.test"}
	name, email := ServerIdentity()
	server := name + " <" + email + ">"

	commit := func(file string, commitOp func() error) {
		t.Helper()
		if err := k.WriteFileAtomic(file, []byte("x\n")); err != nil {
			t.Fatal(err)
		}
		if err := commitOp(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	commit("data/no-author.md", func() error { _, err := k.CommitOp("test: no author"); return err })
	if got, err := gitLogFormat(t, k.Root, "%an <%ae> | %cn <%ce>"); err != nil || got != server+" | "+server {
		t.Fatalf("no author: %q, %v, want the server for both", got, err)
	}
	commit("data/with-author.md", func() error {
		_, err := k.CommitOpAs("test: with author", "Writer Person", "writer@example.test")
		return err
	})
	if got, err := gitLogFormat(t, k.Root, "%an <%ae> | %cn <%ce>"); err != nil || got != "Writer Person <writer@example.test> | Writer Person <writer@example.test>" {
		t.Fatalf("with author: %q, %v", got, err)
	}
	k.SetDefaultIdentity("Configured One", "conf@example.test")
	commit("data/configured.md", func() error { _, err := k.CommitOp("test: configured"); return err })
	if got, err := gitLogFormat(t, k.Root, "%an <%ae> | %cn <%ce>"); err != nil || got != "Configured One <conf@example.test> | Configured One <conf@example.test>" {
		t.Fatalf("configured identity: %q, %v", got, err)
	}
}

func TestServerIdentity_HostIsAValidEmailDomain(t *testing.T) {
	t.Parallel()
	for host, want := range map[string]string{
		"Build-Host.Example.COM": "build-host.example.com",
		"my host_01":             "my-host-01",
		"":                       "localhost",
		"...":                    "localhost",
	} {
		if got := identityHost(host); got != want {
			t.Errorf("identityHost(%q) = %q, want %q", host, got, want)
		}
	}
	if _, email := ServerIdentity(); !strings.HasPrefix(email, "cartographer@") {
		t.Errorf("server email = %q", email)
	}
}

func TestGitStatusSnapshot_NoRemote(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	if s := k.GitStatusSnapshot(); s.State != "no_remote" {
		t.Fatalf("status = %+v", s)
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
// working tree is already clean.
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
