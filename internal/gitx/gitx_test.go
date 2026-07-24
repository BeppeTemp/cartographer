package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func TestInitAndCommit(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}

	dir, err := os.MkdirTemp("", "wiki-gitx-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	// Configure local git identity for the test (required in CI environments without global config).
	runGit(dir, "config", "user.email", "test@wiki.local")
	runGit(dir, "config", "user.name", "Wiki Test")

	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !IsRepo(dir) {
		t.Fatal("IsRepo: must return true after Init")
	}

	// Create a file and commit.
	f := filepath.Join(dir, "index.md")
	if err := os.WriteFile(f, []byte("# Index\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Configure local identity in the repo.
	runGit(dir, "config", "user.email", "test@wiki.local")
	runGit(dir, "config", "user.name", "Wiki Test")

	err = Commit(dir, "test: primo commit", "Wiki Test", "test@wiki.local")
	if err != nil && err != ErrNothingToCommit {
		t.Fatalf("Commit: %v", err)
	}

	sha, err := HeadSHA(dir)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if sha == "" {
		t.Fatal("HeadSHA: empty sha")
	}
}

func TestCommit_NothingToCommit(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}

	dir, err := os.MkdirTemp("", "wiki-gitx-empty-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Create and commit a file.
	f := filepath.Join(dir, "file.md")
	os.WriteFile(f, []byte("contenuto"), 0o644)
	runGit(dir, "config", "user.email", "test@wiki.local")
	runGit(dir, "config", "user.name", "Wiki Test")
	Commit(dir, "initial", "Wiki Test", "test@wiki.local")

	// Second commit without changes: must return ErrNothingToCommit.
	err = Commit(dir, "empty", "Wiki Test", "test@wiki.local")
	if err != ErrNothingToCommit {
		t.Fatalf("Commit without changes: expected ErrNothingToCommit, got %v", err)
	}
}

func TestLogNameStatus(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}

	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	first := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	second := first.Add(time.Hour)
	path := filepath.Join(dir, "data", "before.md")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := Commit(dir, "concept_write: before", "First Author", "first@example.test",
		"GIT_AUTHOR_DATE="+first.Format(time.RFC3339), "GIT_COMMITTER_DATE="+first.Format(time.RFC3339)); err != nil {
		t.Fatalf("Commit first: %v", err)
	}

	if err := os.Rename(path, filepath.Join(dir, "data", "after.md")); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := Commit(dir, "concept_move: after", "Second Author", "second@example.test",
		"GIT_AUTHOR_DATE="+second.Format(time.RFC3339), "GIT_COMMITTER_DATE="+second.Format(time.RFC3339)); err != nil {
		t.Fatalf("Commit rename: %v", err)
	}

	commits, err := LogNameStatus(dir, first.Add(-time.Second))
	if err != nil {
		t.Fatalf("LogNameStatus: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("LogNameStatus commits = %d, want 2: %#v", len(commits), commits)
	}
	if commits[0].Subject != "concept_move: after" || commits[0].Author != "Second Author" {
		t.Fatalf("newest commit = %#v", commits[0])
	}
	if len(commits[0].Files) != 1 {
		t.Fatalf("rename files = %#v", commits[0].Files)
	}
	got := commits[0].Files[0]
	if got.Status != "R" || got.OldPath != "data/before.md" || got.Path != "data/after.md" {
		t.Errorf("rename = %#v, want R data/before.md -> data/after.md", got)
	}
	if commits[1].Files[0].Status != "A" || commits[1].Files[0].Path != "data/before.md" {
		t.Errorf("add = %#v, want A data/before.md", commits[1].Files[0])
	}
}

func TestLogNameStatus_EmptyHistory(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}

	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	commits, err := LogNameStatus(dir, time.Now())
	if err != nil {
		t.Fatalf("LogNameStatus empty repository: %v", err)
	}
	if len(commits) != 0 {
		t.Fatalf("LogNameStatus empty repository = %#v, want no commits", commits)
	}
}

// TestCommit_AuthorAndCommitterIdentity verifies that Commit sets both the
// author (from its authorName/authorEmail parameters) and the committer
// (from GIT_COMMITTER_NAME/EMAIL in env) on the resulting commit.
func TestCommit_AuthorAndCommitterIdentity(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}

	dir, err := os.MkdirTemp("", "wiki-gitx-identity-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Base identity so any commit made without an explicit committer env
	// still succeeds (not exercised here, but keeps the repo well-formed).
	runGit(dir, "config", "user.email", "base@wiki.local")
	runGit(dir, "config", "user.name", "Base User")

	f := filepath.Join(dir, "index.md")
	if err := os.WriteFile(f, []byte("# Index\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	env := []string{"GIT_COMMITTER_NAME=Committer Bot", "GIT_COMMITTER_EMAIL=committer@wiki.local"}
	err = Commit(dir, "test: identity per-KB", "Author Person", "author@wiki.local", env...)
	if err != nil && err != ErrNothingToCommit {
		t.Fatalf("Commit: %v", err)
	}

	out, err := runGit(dir, "log", "-1", "--format=%an <%ae> %cn <%ce>")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	got := strings.TrimSpace(out)
	want := "Author Person <author@wiki.local> Committer Bot <committer@wiki.local>"
	if got != want {
		t.Fatalf("git log identity = %q, want %q", got, want)
	}
}

// TestRunGitEnv_PassesEnv verifies that runGitEnv makes the extra env
// entries visible to the git subprocess (via a committer identity round-trip,
// since git has no plain "print env" subcommand).
func TestRunGitEnv_PassesEnv(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}

	dir, err := os.MkdirTemp("", "wiki-gitx-runenv-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	runGit(dir, "config", "user.email", "base@wiki.local")
	runGit(dir, "config", "user.name", "Base User")

	f := filepath.Join(dir, "f.txt")
	os.WriteFile(f, []byte("content\n"), 0o644)
	runGit(dir, "add", "-A")

	env := []string{"GIT_AUTHOR_NAME=Env Author", "GIT_AUTHOR_EMAIL=env@wiki.local"}
	out, err := runGitEnv(dir, env, "commit", "-m", "env test")
	if err != nil {
		t.Fatalf("runGitEnv commit: %v: %s", err, out)
	}

	logOut, err := runGit(dir, "log", "-1", "--format=%an <%ae>")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	got := strings.TrimSpace(logOut)
	want := "Env Author <env@wiki.local>"
	if got != want {
		t.Fatalf("git log author = %q, want %q (runGitEnv did not propagate env)", got, want)
	}
}

func TestIsRepo_False(t *testing.T) {
	dir, err := os.MkdirTemp("", "wiki-not-git-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	if IsRepo(dir) {
		t.Fatal("IsRepo: must return false for non-git directory")
	}
}
