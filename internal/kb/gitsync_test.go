package kb

import (
	"os/exec"
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

// TestCommitOp_AutoCommitEnabled_DirtyTree verifies that CommitOp with
// AutoCommit=true on a dirty working tree creates a new commit.
func TestCommitOp_AutoCommitEnabled_DirtyTree(t *testing.T) {
	k, sha1 := initGitKB(t)
	k.AutoCommit = true

	if err := k.WriteFileAtomic("data/gitsync-note.md", []byte("---\ntype: Note\ntitle: GitSync\n---\ntest\n")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	if err := k.CommitOp("test: gitsync dirty tree"); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}

	sha2, _ := gitx.HeadSHA(k.Root)
	if sha1 == sha2 {
		t.Fatal("CommitOp: expected new commit but HEAD SHA is unchanged")
	}
}

// TestCommitOp_AutoCommitDisabled_NoCommit verifies that CommitOp with
// AutoCommit=false (the zero-value default) never creates a commit.
func TestCommitOp_AutoCommitDisabled_NoCommit(t *testing.T) {
	k, sha1 := initGitKB(t)
	// AutoCommit defaults to false — do not set it.

	if err := k.WriteFileAtomic("data/gitsync-note.md", []byte("---\ntype: Note\ntitle: GitSync\n---\ntest\n")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	if err := k.CommitOp("test: gitsync disabled"); err != nil {
		t.Fatalf("CommitOp unexpected error: %v", err)
	}

	sha2, _ := gitx.HeadSHA(k.Root)
	if sha1 != sha2 {
		t.Fatal("CommitOp: expected no commit (AutoCommit=false) but HEAD SHA changed")
	}
}

// TestCommitOp_PerKBIdentity verifies that CommitOp uses k.GitAuthorName/
// GitAuthorEmail as the commit author and picks up the committer from
// k.GitEnv (GIT_COMMITTER_NAME/EMAIL).
func TestCommitOp_PerKBIdentity(t *testing.T) {
	k, _ := initGitKB(t)
	k.AutoCommit = true
	k.GitAuthorName = "Author Person"
	k.GitAuthorEmail = "author@wiki.local"
	k.GitEnv = []string{"GIT_COMMITTER_NAME=Committer Bot", "GIT_COMMITTER_EMAIL=committer@wiki.local"}

	if err := k.WriteFileAtomic("data/gitsync-identity.md", []byte("---\ntype: Note\ntitle: Identity\n---\ntest\n")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if err := k.CommitOp("test: per-KB identity"); err != nil {
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
	k, _ := initGitKB(t)
	k.AutoCommit = true
	// GitAuthorName/GitAuthorEmail/GitEnv left at zero value.

	if err := k.WriteFileAtomic("data/gitsync-default-identity.md", []byte("---\ntype: Note\ntitle: Default\n---\ntest\n")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if err := k.CommitOp("test: default identity"); err != nil {
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
	k, sha1 := initGitKB(t)
	k.AutoCommit = true

	// Working tree is clean after Init — CommitOp should be a no-op.
	if err := k.CommitOp("test: gitsync clean tree"); err != nil {
		t.Fatalf("CommitOp unexpected error: %v", err)
	}

	sha2, _ := gitx.HeadSHA(k.Root)
	if sha1 != sha2 {
		t.Fatal("CommitOp: expected no commit on clean tree but HEAD SHA changed")
	}
}
