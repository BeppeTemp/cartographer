package gitx

import (
	"context"
	"errors"
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

// TestCloneEnv: the non-interactive defaults are always added, and an
// operator's own GIT_SSH_COMMAND is never overwritten — they have said how to
// reach their forge, and replacing that breaks a working setup to prevent a
// hypothetical one.
func TestCloneEnv(t *testing.T) {
	const sshRemote = "git@forge.example.com:team/kb.git"
	env := cloneEnv(sshRemote, nil)
	if !hasEnv(env, "GIT_TERMINAL_PROMPT") {
		t.Error("GIT_TERMINAL_PROMPT must always be set: git must fail rather than prompt")
	}
	if !strings.Contains(strings.Join(env, "\n"), "BatchMode=yes") {
		t.Error("an ssh remote should get BatchMode + ConnectTimeout")
	}
	if strings.Contains(strings.Join(env, "\n"), "StrictHostKeyChecking") {
		t.Error("host-key policy must be left to the operator's ssh config")
	}

	caller := "GIT_SSH_COMMAND=ssh -i /keys/id_ed25519"
	env = cloneEnv(sshRemote, []string{caller})
	var got []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_SSH_COMMAND=") {
			got = append(got, kv)
		}
	}
	if len(got) != 1 || got[0] != caller {
		t.Errorf("caller's GIT_SSH_COMMAND was not preserved: %v", got)
	}

	// An https remote needs no ssh wrapper at all.
	env = cloneEnv("https://forge.example.com/team/kb.git", nil)
	if hasEnv(env, "GIT_SSH_COMMAND") && !hasEnv(os.Environ(), "GIT_SSH_COMMAND") {
		t.Error("an https remote should not get GIT_SSH_COMMAND")
	}
}

func TestIsSSHRemote(t *testing.T) {
	cases := map[string]bool{
		"git@forge.example.com:team/kb.git":     true,
		"ssh://git@forge.example.com/team/kb":   true,
		"https://forge.example.com/team/kb.git": false,
		"/srv/git/kb.git":                       false,
		"../local/kb":                           false,
	}
	for remote, want := range cases {
		if got := isSSHRemote(remote); got != want {
			t.Errorf("isSSHRemote(%q) = %v, want %v", remote, got, want)
		}
	}
}

// TestClone_ContextCancelled: a clone stopped by its deadline reports
// ErrCloneTimeout, so the caller can name the flag that raises the budget
// instead of showing git's own wording for a killed process.
func TestClone_ContextCancelled(t *testing.T) {
	if !hasGit() {
		t.Skip("git not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Clone(ctx, "https://forge.invalid/team/kb.git", filepath.Join(t.TempDir(), "dest"))
	if !errors.Is(err, ErrCloneTimeout) {
		t.Fatalf("err = %v, want ErrCloneTimeout", err)
	}
}

// TestClone_UnresolvableHost: the failure is translated into something with a
// remedy, and it happens within the deadline rather than hanging on a prompt.
func TestClone_UnresolvableHost(t *testing.T) {
	if !hasGit() {
		t.Skip("git not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dest := filepath.Join(t.TempDir(), "dest")
	start := time.Now()
	err := Clone(ctx, "https://cartographer-nonexistent.invalid/team/kb.git", dest)
	if err == nil {
		t.Fatal("clone from an unresolvable host should fail")
	}
	if errors.Is(err, ErrCloneTimeout) {
		t.Fatalf("should have failed on its own, not on the deadline: %v", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Error("clone outlived its context")
	}
	// The raw git output is always kept; the remedy is added when recognised.
	if !strings.Contains(err.Error(), "git clone") {
		t.Errorf("error does not name the operation: %v", err)
	}
}

// TestClone_LocalBareRepo is the success path, and pins that --progress and
// the captured stderr do not break a working clone.
func TestClone_LocalBareRepo(t *testing.T) {
	if !hasGit() {
		t.Skip("git not available")
	}
	origin := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", DefaultBranch, origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	work := t.TempDir()
	if err := Init(work); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Commit(work, "seed", "T", "t@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := AddRemote(work, "origin", origin); err != nil {
		t.Fatal(err)
	}
	if err := PushSetUpstream(work, "origin", DefaultBranch); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone(context.Background(), origin, dest); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Errorf("cloned tree is missing the committed file: %v", err)
	}
}
