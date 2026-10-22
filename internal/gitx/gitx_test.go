package gitx

import (
	"context"
	"errors"
	"fmt"
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
	// cloneEnv starts from os.Environ(), so an inherited GIT_SSH_COMMAND would
	// count as a second caller value. The go command sets one itself when it
	// switches toolchain (go.mod newer than the installed go), which is how
	// this test failed only on such machines. t.Setenv restores it afterwards.
	t.Setenv("GIT_SSH_COMMAND", "")
	os.Unsetenv("GIT_SSH_COMMAND")

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
	if hasEnv(env, "GIT_SSH_COMMAND") {
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

func TestProbeRemote(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	ctx := context.Background()

	empty := filepath.Join(t.TempDir(), "empty.git")
	if out, err := exec.Command("git", "init", "--bare", empty).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	if has, err := ProbeRemote(ctx, empty); err != nil || has {
		t.Errorf("ProbeRemote(empty bare repo) = (%v, %v), want (false, nil)", has, err)
	}

	src := t.TempDir()
	if err := Init(src); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Commit(src, "init", "t", "t@example.com"); err != nil {
		t.Fatal(err)
	}
	if has, err := ProbeRemote(ctx, src); err != nil || !has {
		t.Errorf("ProbeRemote(repo with a commit) = (%v, %v), want (true, nil)", has, err)
	}

	if _, err := ProbeRemote(ctx, filepath.Join(t.TempDir(), "missing.git")); err == nil {
		t.Error("ProbeRemote(missing path) returned no error")
	}
}

func TestLogNameStatus_Reason(t *testing.T) {
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
	base := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	commit := func(i int, msg string) {
		t.Helper()
		name := filepath.Join(dir, "data", fmt.Sprintf("f%d.md", i))
		if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		if err := Commit(dir, msg, "Author", "a@example.test", "GIT_AUTHOR_DATE="+at, "GIT_COMMITTER_DATE="+at); err != nil {
			t.Fatalf("Commit %q: %v", msg, err)
		}
	}
	commit(1, "concept_write: plain")
	commit(2, "concept_write: it's \"quoted\" % $x `y`\n\nReason: incident 42: DNS cutover")
	commit(3, "concept_write: multi\n\nReason: first\nReason: second")

	commits, err := LogNameStatus(dir, base.Add(-time.Second))
	if err != nil {
		t.Fatalf("LogNameStatus: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("commits = %d, want 3: %#v", len(commits), commits)
	}
	want := []struct{ subject, reason, path string }{
		{"concept_write: multi", "first", "data/f3.md"},
		{"concept_write: it's \"quoted\" % $x `y`", "incident 42: DNS cutover", "data/f2.md"},
		{"concept_write: plain", "", "data/f1.md"},
	}
	for i, w := range want {
		c := commits[i]
		if c.Subject != w.subject || c.Reason != w.reason {
			t.Errorf("commit %d = subject %q reason %q, want %q / %q", i, c.Subject, c.Reason, w.subject, w.reason)
		}
		if len(c.Files) != 1 || c.Files[0].Path != w.path || c.Files[0].Status != "A" {
			t.Errorf("commit %d files = %#v, want A %s", i, c.Files, w.path)
		}
	}
}

// commitAt commits the working tree at a fixed instant with an optional
// Reason trailer in the message.
func commitAt(t *testing.T, dir, msg string, at time.Time) {
	t.Helper()
	ts := at.Format(time.RFC3339)
	if err := Commit(dir, msg, "Tester", "tester@example.test", "GIT_AUTHOR_DATE="+ts, "GIT_COMMITTER_DATE="+ts); err != nil {
		t.Fatalf("Commit %q: %v", msg, err)
	}
}

func TestFileHistory(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2024, time.March, 1, 10, 0, 0, 0, time.UTC)
	old := filepath.Join(dir, "data", "old.md")
	content := "line one\nline two\nline three\nline four\n"
	if err := os.WriteFile(old, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAt(t, dir, "concept_write: old", base)
	if err := os.WriteFile(old, []byte(content+"line five\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAt(t, dir, "concept_write: old\n\nReason: fix the outage note", base.Add(time.Hour))
	if err := os.Rename(old, filepath.Join(dir, "data", "new.md")); err != nil {
		t.Fatal(err)
	}
	commitAt(t, dir, "concept_move: new", base.Add(2*time.Hour))
	if err := os.WriteFile(filepath.Join(dir, "data", "other.md"), []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAt(t, dir, "concept_write: other", base.Add(3*time.Hour))

	revs, err := FileHistory(dir, "data/new.md", 10)
	if err != nil {
		t.Fatalf("FileHistory: %v", err)
	}
	if len(revs) != 3 {
		t.Fatalf("revisions = %d, want 3 (rename followed): %#v", len(revs), revs)
	}
	if revs[0].Status != "R" || revs[0].Path != "data/new.md" || revs[0].Subject != "concept_move: new" {
		t.Errorf("newest = %#v, want the rename to data/new.md", revs[0])
	}
	if revs[1].Status != "M" || revs[1].Path != "data/old.md" || revs[1].Reason != "fix the outage note" {
		t.Errorf("middle = %#v, want an edit of data/old.md with its reason", revs[1])
	}
	if revs[2].Status != "A" || revs[2].Reason != "" {
		t.Errorf("oldest = %#v, want the add without a reason", revs[2])
	}

	limited, err := FileHistory(dir, "data/new.md", 2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("limit 2 = %d revisions, err %v", len(limited), err)
	}
	none, err := FileHistory(dir, "data/missing.md", 10)
	if err != nil || len(none) != 0 {
		t.Fatalf("missing path = %#v, err %v; want empty", none, err)
	}
}

func TestFileHistory_NotARepo(t *testing.T) {
	revs, err := FileHistory(t.TempDir(), "a.md", 5)
	if err != nil || len(revs) != 0 {
		t.Fatalf("FileHistory outside a repository = %#v, %v; want empty", revs, err)
	}
}

func TestAcceptedFindingTrailerAndStagedChanges(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, "data", name), []byte("content of "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a b.md")
	if err := Commit(dir, "seed", "Author", "a@example.test"); err != nil {
		t.Fatal(err)
	}

	// StagedChanges: a rename (path with a space), an add and a delete.
	if err := os.Rename(filepath.Join(dir, "data", "a b.md"), filepath.Join(dir, "data", "c d.md")); err != nil {
		t.Fatal(err)
	}
	write("new.md")
	changes, err := StagedChanges(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]FileChange{}
	for _, c := range changes {
		got[c.Path] = c
	}
	if c := got["data/c d.md"]; c.Status != "R" || c.OldPath != "data/a b.md" {
		t.Errorf("rename = %+v", c)
	}
	if c := got["data/new.md"]; c.Status != "A" {
		t.Errorf("add = %+v", c)
	}

	// DiscardChanges returns to HEAD, untracked files included.
	if err := DiscardChanges(dir); err != nil {
		t.Fatal(err)
	}
	if st, _ := Status(dir); strings.TrimSpace(st) != "" {
		t.Errorf("tree not clean after DiscardChanges: %q", st)
	}

	// The trailer round-trips through LogNameStatus and FileHistory; a commit
	// without it, or with only a Reason, reports none.
	write("t.md")
	msg := "concept_write: t\n\nReason: why\nAccepted-Finding: duplicate_link: kept on purpose\nAccepted-Finding: bare_link_list: legacy"
	if err := Commit(dir, msg, "Author", "a@example.test"); err != nil {
		t.Fatal(err)
	}
	commits, err := LogNameStatus(dir, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if c := commits[0]; c.Reason != "why" || len(c.AcceptedFindings) != 2 || c.AcceptedFindings[0] != "duplicate_link: kept on purpose" {
		t.Errorf("commit = %+v", c)
	}
	if c := commits[1]; len(c.AcceptedFindings) != 0 {
		t.Errorf("seed commit carries %v", c.AcceptedFindings)
	}
	revs, err := FileHistory(dir, "data/t.md", 0)
	if err != nil || len(revs) != 1 || revs[0].Reason != "why" || len(revs[0].AcceptedFindings) != 2 {
		t.Errorf("FileHistory = %+v (%v)", revs, err)
	}
}

func TestAheadBehind(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	g := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=user@example.com", "-c", "user.name=user"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(dir, name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		g(dir, "add", ".")
		g(dir, "commit", "-q", "-m", name)
	}
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	g(base, "init", "-q", "--bare", "-b", "main", bare)
	work := filepath.Join(base, "work")
	g(base, "clone", "-q", bare, work)
	g(work, "checkout", "-q", "-B", "main")
	commit(work, "a")
	g(work, "push", "-q", "origin", "main")
	other := filepath.Join(base, "other")
	g(base, "clone", "-q", bare, other)

	tests := []struct {
		name          string
		setup         func()
		branch        string
		ahead, behind int
		known         bool
	}{
		{"empty branch", func() {}, "", 0, 0, false},
		{"no tracking ref", func() {}, "nope", 0, 0, false},
		{"equal", func() {}, "main", 0, 0, true},
		{"local ahead 1", func() { commit(work, "b") }, "main", 1, 0, true},
		{"remote ahead 1 after fetch only", func() {
			g(work, "reset", "-q", "--hard", "origin/main")
			commit(other, "c")
			g(other, "push", "-q", "origin", "main")
			g(work, "fetch", "-q", "origin")
		}, "main", 0, 1, true},
		{"diverged", func() { commit(work, "d") }, "main", 1, 1, true},
	}
	for _, tt := range tests {
		tt.setup()
		ahead, behind, known, err := AheadBehind(work, "origin", tt.branch)
		if err != nil || ahead != tt.ahead || behind != tt.behind || known != tt.known {
			t.Errorf("%s: got (%d,%d,%v,%v), want (%d,%d,%v,nil)", tt.name, ahead, behind, known, err, tt.ahead, tt.behind, tt.known)
		}
	}
}

func TestIsForgeInitPath(t *testing.T) {
	for p, want := range map[string]bool{
		"README.md": true, "readme": true, "LICENSE": true, "LICENSE.txt": true,
		"COPYING": true, ".gitignore": true, ".gitattributes": true,
		"sub/README.md": false, "index.md": false, "README-old/x": false, "": false,
	} {
		if got := isForgeInitPath(p); got != want {
			t.Errorf("isForgeInitPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestForgeInitialized(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	build := func(files map[string]string, extraCommits int) string {
		dir := t.TempDir()
		if err := Init(dir); err != nil {
			t.Fatal(err)
		}
		runGit(dir, "config", "user.email", "t@example.com")
		runGit(dir, "config", "user.name", "T")
		for name, body := range files {
			full := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		runGit(dir, "add", ".")
		if out, err := runGit(dir, "commit", "-m", "Initial commit"); err != nil {
			t.Fatalf("commit: %v: %s", err, out)
		}
		for i := 0; i < extraCommits; i++ {
			runGit(dir, "commit", "--allow-empty", "-m", "more")
		}
		return dir
	}
	cases := []struct {
		name  string
		files map[string]string
		extra int
		want  bool
	}{
		{"readme+license", map[string]string{"README.md": "a", "LICENSE": "b"}, 0, true},
		{"two commits", map[string]string{"README.md": "a"}, 1, false},
		{"source file", map[string]string{"src/main.go": "package main"}, 0, false},
	}
	for _, c := range cases {
		dir := build(c.files, c.extra)
		_, ok, err := ForgeInitialized(dir, "HEAD")
		if err != nil || ok != c.want {
			t.Errorf("%s: ForgeInitialized = (%v, %v), want (%v, nil)", c.name, ok, err, c.want)
		}
	}
	if _, _, err := ForgeInitialized(t.TempDir(), "HEAD"); err == nil {
		t.Error("ForgeInitialized outside a repo returned no error")
	}
}

func TestForgeBoilerplateReadme(t *testing.T) {
	fixture := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", "forge-readmes", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	const desc = "A short project description."
	cases := []struct {
		name, repo, text string
		want             bool
		wantDesc         string
	}{
		{"github", "kb-a", fixture("github.md"), true, desc},
		{"github title only", "kb-a", fixture("github-title-only.md"), true, ""},
		{"gitea", "kb-a", fixture("gitea.md"), true, desc},
		{"forgejo", "kb-a", fixture("forgejo.md"), true, desc},
		{"gitlab", "kb-a", fixture("gitlab.md"), true, ""},
		{"title case differs", "KB-A", fixture("gitea.md"), true, desc},
		{"title differs from the repo", "kb-b", fixture("gitea.md"), false, ""},
		{"gitlab title differs", "kb-b", fixture("gitlab.md"), false, ""},
		{"two paragraphs", "kb-a", "# kb-a\n\nOne.\n\nTwo.\n", false, ""},
		{"description and list", "kb-a", "# kb-a\n\nOne.\n- item\n", false, ""},
		{"hand-written starting with the name", "kb-a", "# kb-a\n\nNotes on how we run kb-a.\n\n## Usage\n\nRead the index.\n", false, ""},
		{"gitlab without a marker", "kb-a", strings.Replace(fixture("gitlab.md"), "## Add your files", "## Add files", 1), false, ""},
		{"gitlab without the intro", "kb-a", strings.Replace(fixture("gitlab.md"), "get started with GitLab", "start", 1), false, ""},
		{"empty", "kb-a", "\n\n", false, ""},
		{"no repo name", "", "# \n", false, ""},
	}
	for _, c := range cases {
		for variant, text := range map[string]string{
			"lf":   c.text,
			"crlf": strings.ReplaceAll(c.text, "\n", "\r\n"),
			"bom":  "\ufeff" + c.text,
		} {
			got, gotDesc := ForgeBoilerplateReadme(c.repo, text)
			if got != c.want || gotDesc != c.wantDesc {
				t.Errorf("%s/%s: ForgeBoilerplateReadme = (%v, %q), want (%v, %q)", c.name, variant, got, gotDesc, c.want, c.wantDesc)
			}
		}
	}
}

func TestForgeReadmePath(t *testing.T) {
	for _, c := range []struct {
		paths []string
		want  string
		ok    bool
	}{
		{[]string{"README.md", "LICENSE"}, "README.md", true},
		{[]string{"readme.md"}, "readme.md", true},
		{[]string{"README.rst", "LICENSE"}, "", false},
		{[]string{"README"}, "", false},
		{[]string{"README.md", "README.txt"}, "", false},
		{[]string{"README.md", "readme.md"}, "", false},
		{[]string{"LICENSE"}, "", false},
	} {
		got, ok := ForgeReadmePath(c.paths)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ForgeReadmePath(%v) = (%q, %v), want (%q, %v)", c.paths, got, ok, c.want, c.ok)
		}
	}
}

func TestRepoNameFromURL(t *testing.T) {
	for url, want := range map[string]string{
		"https://example.com/user/kb-a.git":         "kb-a",
		"https://example.com/user/kb-a/":            "kb-a",
		"https://example.com/user/kb-a":             "kb-a",
		"ssh://git@example.com:2222/group/kb-a.git": "kb-a",
		"git@example.com:group/sub/kb-a.git":        "kb-a",
		"git@example.com:kb-a.git":                  "kb-a",
		"file:///srv/forge/kb-a.git":                "kb-a",
	} {
		if got := RepoNameFromURL(url); got != want {
			t.Errorf("RepoNameFromURL(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestLogNameStatusAndFileHistory_Agent(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	base := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	msgs := []string{
		"concept_write: both\n\nReason: why\nAgent: client-a",
		"concept_write: agent only\n\nAgent: client-b\nAgent: client-c",
		"concept_write: neither",
	}
	for i, msg := range msgs {
		if err := os.WriteFile(filepath.Join(dir, "f.md"), []byte(fmt.Sprintf("v%d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		if err := Commit(dir, msg, "Author", "a@example.test", "GIT_AUTHOR_DATE="+at, "GIT_COMMITTER_DATE="+at); err != nil {
			t.Fatal(err)
		}
	}
	want := []struct{ agent, reason string }{{"", ""}, {"client-b", ""}, {"client-a", "why"}}

	commits, err := LogNameStatus(dir, base.Add(-time.Second))
	if err != nil || len(commits) != 3 {
		t.Fatalf("LogNameStatus = %d commits, err %v", len(commits), err)
	}
	for i, w := range want {
		c := commits[i]
		if c.Agent != w.agent || c.Reason != w.reason || len(c.Files) != 1 || c.Files[0].Path != "f.md" {
			t.Errorf("commit %d = agent %q reason %q files %#v, want %q / %q", i, c.Agent, c.Reason, c.Files, w.agent, w.reason)
		}
	}

	revs, err := FileHistory(dir, "f.md", 0)
	if err != nil || len(revs) != 3 {
		t.Fatalf("FileHistory = %d revisions, err %v", len(revs), err)
	}
	for i, w := range want {
		r := revs[i]
		if r.Agent != w.agent || r.Reason != w.reason || r.Path != "f.md" {
			t.Errorf("revision %d = agent %q reason %q path %q, want %q / %q", i, r.Agent, r.Reason, r.Path, w.agent, w.reason)
		}
	}
}

// The Cartographer-Token trailer (D904) is read back by both log readers, the
// last one winning, and does not displace the Reason that follows it in the
// format.
func TestLogNameStatusAndFileHistory_Token(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	base := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	msgs := []string{
		"concept_write: one\n\nReason: why\nAgent: client-a\nCartographer-Token: tok-a",
		"concept_write: two\n\nCartographer-Token: tok-b\nCartographer-Token: tok-c",
		"concept_write: none",
	}
	for i, msg := range msgs {
		if err := os.WriteFile(filepath.Join(dir, "f.md"), []byte(fmt.Sprintf("v%d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		if err := Commit(dir, msg, "Author", "a@example.test", "GIT_AUTHOR_DATE="+at, "GIT_COMMITTER_DATE="+at); err != nil {
			t.Fatal(err)
		}
	}
	want := []struct{ token, reason string }{{"", ""}, {"tok-c", ""}, {"tok-a", "why"}}

	commits, err := LogNameStatus(dir, base.Add(-time.Second))
	if err != nil || len(commits) != 3 {
		t.Fatalf("LogNameStatus = %d commits, err %v", len(commits), err)
	}
	revs, err := FileHistory(dir, "f.md", 0)
	if err != nil || len(revs) != 3 {
		t.Fatalf("FileHistory = %d revisions, err %v", len(revs), err)
	}
	for i, w := range want {
		if c := commits[i]; c.Token != w.token || c.Reason != w.reason || len(c.Files) != 1 {
			t.Errorf("commit %d = token %q reason %q files %d, want %q / %q", i, c.Token, c.Reason, len(c.Files), w.token, w.reason)
		}
		if r := revs[i]; r.Token != w.token || r.Reason != w.reason {
			t.Errorf("revision %d = token %q reason %q, want %q / %q", i, r.Token, r.Reason, w.token, w.reason)
		}
	}
}

// TestStashPushAllEnv_NoIdentityOnHost checks that a stash of uncommitted
// work succeeds on a host with no git identity (no user.name/user.email in
// the repo, the global config or the system config), when the caller passes
// the server identity. git stash creates commits, so without it it fails with
// "Committer identity unknown".
func TestStashPushAllEnv_NoIdentityOnHost(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}

	dir := t.TempDir()
	home := t.TempDir()
	// No identity anywhere git looks: empty home, no system config.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "missing-gitconfig"))
	t.Setenv("GIT_AUTHOR_NAME", "")
	t.Setenv("GIT_AUTHOR_EMAIL", "")
	t.Setenv("GIT_COMMITTER_NAME", "")
	t.Setenv("GIT_COMMITTER_EMAIL", "")
	t.Setenv("EMAIL", "")
	t.Setenv("USER", "")
	t.Setenv("USERNAME", "")

	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ident := []string{"GIT_AUTHOR_NAME=Server", "GIT_AUTHOR_EMAIL=server@wiki.local",
		"GIT_COMMITTER_NAME=Server", "GIT_COMMITTER_EMAIL=server@wiki.local"}
	f := filepath.Join(dir, "index.md")
	if err := os.WriteFile(f, []byte("# Index\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := Commit(dir, "init", "Server", "server@wiki.local", ident...); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Uncommitted work, tracked and untracked.
	if err := os.WriteFile(f, []byte("# Index\nchanged\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := StashPushAllEnv(dir, ident...); err != nil {
		t.Fatalf("StashPushAllEnv with server identity: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.md")); !os.IsNotExist(err) {
		t.Fatalf("untracked file should be stashed away, stat err = %v", err)
	}
	if err := StashPop(dir); err != nil {
		t.Fatalf("StashPop: %v", err)
	}
	if got, _ := os.ReadFile(f); string(got) != "# Index\nchanged\n" {
		t.Fatalf("restored index.md = %q", got)
	}
}

func TestValidateRemote(t *testing.T) {
	ok := []string{
		"git@forge.example.com:team/kb.git",
		"ssh://git@forge.example.com:2222/team/kb.git",
		"https://forge.example.com/team/kb.git",
		"HTTP://forge.example.com/team/kb.git",
		"git://forge.example.com/team/kb.git",
		"file:///srv/kb.git",
		"git@[::1]:repo.git",
		"ssh://[::1]/x",
		"/srv/kb.git",
		"./relative/kb.git",
	}
	for _, r := range ok {
		if err := ValidateRemote(r); err != nil {
			t.Errorf("ValidateRemote(%q) = %v, want nil", r, err)
		}
	}
	bad := []string{
		"", "-oProxyCommand=x", "--upload-pack=x", "ext::sh -c id", "fd::3", "[::1]::x",
		"http::https://example.com/r", "ftp://h/r", "rsync://h/r", "https://h/r\nx",
	}
	for _, r := range bad {
		if err := ValidateRemote(r); err == nil {
			t.Errorf("ValidateRemote(%q) = nil, want an error", r)
		}
	}
}

func TestRemoteHelpersAreRefused(t *testing.T) {
	if !hasGit() {
		t.Skip("git not available")
	}
	marker := filepath.Join(t.TempDir(), "pwned")
	helper := "ext::touch " + marker

	if err := Clone(context.Background(), helper, filepath.Join(t.TempDir(), "c")); err == nil {
		t.Error("Clone accepted a transport helper")
	}
	if _, err := ProbeRemote(context.Background(), helper); err == nil {
		t.Error("ProbeRemote accepted a transport helper")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the transport helper ran")
	}

	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if err := AddRemote(dir, "origin", "--upload-pack=x"); err == nil {
		t.Error("AddRemote accepted an option-like URL")
	}
	if out, _ := exec.Command("git", "-C", dir, "config", "--get-regexp", `^remote\.`).Output(); len(out) != 0 {
		t.Errorf("a remote was configured: %s", out)
	}
	// Pins that this git accepts "--" in "remote add" for a valid URL.
	if err := AddRemote(dir, "origin", "https://forge.example.com/team/kb.git"); err != nil {
		t.Errorf("AddRemote with a valid URL: %v", err)
	}
}

func TestCloneEnv_AllowProtocol(t *testing.T) {
	t.Setenv("GIT_ALLOW_PROTOCOL", "")
	os.Unsetenv("GIT_ALLOW_PROTOCOL")
	if env := cloneEnv("/srv/kb.git", nil); !hasEnv(env, "GIT_ALLOW_PROTOCOL") {
		t.Error("GIT_ALLOW_PROTOCOL must be set when nobody provided one")
	}
	env := cloneEnv("/srv/kb.git", []string{"GIT_ALLOW_PROTOCOL=file"})
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_ALLOW_PROTOCOL=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("caller's GIT_ALLOW_PROTOCOL must be kept alone, got %d entries", n)
	}
}
