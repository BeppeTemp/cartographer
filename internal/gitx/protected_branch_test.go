package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestPush_ProtectedBranchRejectionIsReportedAsSuch simulates a branch
// protection with a pre-receive hook on a bare remote that declines the push,
// and checks that Push wraps ErrProtectedBranch rather than a generic error
// (#903 WP2). The detection is provider-neutral, so the hook prints the
// generic "pre-receive hook declined" wording.
func TestPush_ProtectedBranchRejectionIsReportedAsSuch(t *testing.T) {
	// A bare remote with a pre-receive hook that always refuses.
	remote := t.TempDir()
	if _, err := runGit(remote, "init", "--bare"); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	hook := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'remote: error: GH006: Protected branch update failed' >&2\necho 'pre-receive hook declined' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	// A working clone with one commit on main.
	work := t.TempDir()
	if _, err := runGit(work, "init", "-b", "main"); err != nil {
		t.Fatalf("init work: %v", err)
	}
	runGit(work, "config", "user.email", "test@wiki.local")
	runGit(work, "config", "user.name", "Wiki Test")
	if err := os.WriteFile(filepath.Join(work, "a.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(work, "add", "-A")
	runGit(work, "commit", "-qm", "first")
	runGit(work, "remote", "add", "origin", remote)

	err := Push(work, "origin", "main")
	if err == nil {
		t.Fatal("push to a protected branch must fail")
	}
	if !errors.Is(err, ErrProtectedBranch) {
		t.Errorf("want ErrProtectedBranch, got %v", err)
	}
}

// TestIsProtectedBranchRejection covers the provider-neutral recognition.
func TestIsProtectedBranchRejection(t *testing.T) {
	for _, s := range []string{
		"remote: error: GH006: Protected branch update failed",
		"remote: error: this is a protected branch",
		"remote: pre-receive hook declined",
	} {
		if !isProtectedBranchRejection(s) {
			t.Errorf("should recognise a protected-branch rejection: %q", s)
		}
	}
	if isProtectedBranchRejection("fatal: non-fast-forward") {
		t.Error("a non-fast-forward is not a protected-branch rejection")
	}
}
