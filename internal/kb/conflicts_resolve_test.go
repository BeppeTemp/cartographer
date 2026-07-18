package kb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tgit runs a git command in dir with a deterministic identity and fails the test on error.
func tgit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// setupDivergedKB builds a KB whose foo concept has diverged between a "local" HEAD and
// a "remote" SHA, returning the KB, the local SHA, the remote SHA and the branch name.
func setupDivergedKB(t *testing.T) (k *KB, localSHA, remoteSHA, branch string, fooPath string) {
	t.Helper()
	root := t.TempDir()
	var err error
	k, err = Init(root)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	dataDir := filepath.Join(root, "data")
	fooPath = filepath.Join(dataDir, "foo.md")

	// Base version of the concept.
	if err := os.WriteFile(fooPath, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgit(t, root, "add", "-A")
	tgit(t, root, "commit", "-m", "add foo")
	branch = tgit(t, root, "rev-parse", "--abbrev-ref", "HEAD")

	// Remote line: diverge foo.
	tgit(t, root, "checkout", "-b", "remoteline")
	if err := os.WriteFile(fooPath, []byte("remote version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgit(t, root, "add", "-A")
	tgit(t, root, "commit", "-m", "remote change")
	remoteSHA = tgit(t, root, "rev-parse", "HEAD")

	// Local line: back to base, diverge differently.
	tgit(t, root, "checkout", branch)
	if err := os.WriteFile(fooPath, []byte("local version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgit(t, root, "add", "-A")
	tgit(t, root, "commit", "-m", "local change")
	localSHA = tgit(t, root, "rev-parse", "HEAD")

	return k, localSHA, remoteSHA, branch, fooPath
}

func mustRegisterFoo(t *testing.T, k *KB, localSHA, remoteSHA, branch string) {
	t.Helper()
	if err := k.RegisterConflict(Conflict{
		ConceptID: "foo",
		Path:      "data/foo.md",
		LocalSHA:  localSHA,
		RemoteSHA: remoteSHA,
		Branch:    branch,
		Files:     []string{"data/foo.md"},
	}); err != nil {
		t.Fatalf("RegisterConflict: %v", err)
	}
	if err := k.MarkDegraded("foo"); err != nil {
		// MarkDegraded parses frontmatter; our test file has none, so it may error.
		// Simulate the uncommitted degraded marker directly instead.
		if werr := os.WriteFile(filepath.Join(k.Root, "data", "foo.md"), []byte("local version\nstatus: degraded\n"), 0o644); werr != nil {
			t.Fatalf("simulate degraded: %v", werr)
		}
	}
}

func TestFinalizeConflictsTheirs(t *testing.T) {
	k, localSHA, remoteSHA, branch, fooPath := setupDivergedKB(t)
	mustRegisterFoo(t, k, localSHA, remoteSHA, branch)

	// Resolve as "theirs" → expect the remote version to win.
	if err := k.RecordResolution("foo", "theirs", ""); err != nil {
		t.Fatalf("RecordResolution: %v", err)
	}
	ids, err := k.FinalizeConflicts()
	if err != nil {
		t.Fatalf("FinalizeConflicts: %v", err)
	}
	if len(ids) != 1 || ids[0] != "foo" {
		t.Fatalf("expected ids=[foo], got %v", ids)
	}

	got, _ := os.ReadFile(fooPath)
	if strings.TrimSpace(string(got)) != "remote version" {
		t.Fatalf("expected 'remote version', got %q", string(got))
	}

	// Registry cleared.
	conflicts, _ := k.ListConflicts()
	if len(conflicts) != 0 {
		t.Fatalf("expected empty registry, got %d", len(conflicts))
	}

	// HEAD is a merge commit (two parents).
	parents := strings.Fields(tgit(t, k.Root, "rev-list", "--parents", "-n", "1", "HEAD"))
	if len(parents) != 3 {
		t.Fatalf("expected a merge commit with 2 parents, got %v", parents)
	}
}

func TestFinalizeConflictsOurs(t *testing.T) {
	k, localSHA, remoteSHA, branch, fooPath := setupDivergedKB(t)
	mustRegisterFoo(t, k, localSHA, remoteSHA, branch)

	if err := k.RecordResolution("foo", "ours", ""); err != nil {
		t.Fatalf("RecordResolution: %v", err)
	}
	if _, err := k.FinalizeConflicts(); err != nil {
		t.Fatalf("FinalizeConflicts: %v", err)
	}
	got, _ := os.ReadFile(fooPath)
	if strings.TrimSpace(string(got)) != "local version" {
		t.Fatalf("expected 'local version' (ours), got %q", string(got))
	}
}

func TestFinalizeConflictsEdit(t *testing.T) {
	k, localSHA, remoteSHA, branch, fooPath := setupDivergedKB(t)
	mustRegisterFoo(t, k, localSHA, remoteSHA, branch)

	if err := k.RecordResolution("foo", "edit", "reconciled body\n"); err != nil {
		t.Fatalf("RecordResolution: %v", err)
	}
	if _, err := k.FinalizeConflicts(); err != nil {
		t.Fatalf("FinalizeConflicts: %v", err)
	}
	got, _ := os.ReadFile(fooPath)
	if strings.TrimSpace(string(got)) != "reconciled body" {
		t.Fatalf("expected 'reconciled body' (edit), got %q", string(got))
	}
}

func TestRecordResolutionUnknownConcept(t *testing.T) {
	k, _, _, _, _ := setupDivergedKB(t)
	if err := k.RecordResolution("does-not-exist", "ours", ""); err == nil {
		t.Fatal("expected error for unknown concept, got nil")
	}
}

func TestFinalizePendingBlocks(t *testing.T) {
	k, localSHA, remoteSHA, branch, _ := setupDivergedKB(t)
	mustRegisterFoo(t, k, localSHA, remoteSHA, branch)
	// Register a second conflict with no resolution.
	if err := k.RegisterConflict(Conflict{
		ConceptID: "bar", Path: "data/bar.md", LocalSHA: localSHA, RemoteSHA: remoteSHA, Branch: branch,
	}); err != nil {
		t.Fatal(err)
	}
	if err := k.RecordResolution("foo", "ours", ""); err != nil {
		t.Fatal(err)
	}
	n, _ := k.PendingConflictCount()
	if n != 1 {
		t.Fatalf("expected 1 pending, got %d", n)
	}
	if _, err := k.FinalizeConflicts(); err == nil {
		t.Fatal("expected FinalizeConflicts to refuse with a pending conflict")
	}
}
