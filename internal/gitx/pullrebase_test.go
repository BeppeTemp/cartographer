package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cfgIdentity sets a local git identity so commits succeed in CI environments.
func cfgIdentity(t *testing.T, dir string) {
	t.Helper()
	runGit(dir, "config", "user.email", "test@wiki.local")
	runGit(dir, "config", "user.name", "Wiki Test")
}

func writeFileT(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

// setupClones creates a bare remote with two clones (a, b) sharing one commit.
func setupClones(t *testing.T) (cloneA, cloneB, branch string) {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	if _, err := runGit(base, "init", "--bare", bare); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	cloneA = filepath.Join(base, "a")
	if _, err := runGit(base, "clone", bare, cloneA); err != nil {
		t.Fatalf("clone a: %v", err)
	}
	cfgIdentity(t, cloneA)
	writeFileT(t, cloneA, "f.txt", "line1\n")
	runGit(cloneA, "add", "-A")
	runGit(cloneA, "commit", "-m", "init")
	branch, _ = Branch(cloneA)
	if branch == "" {
		t.Fatal("Branch: empty")
	}
	if _, err := runGit(cloneA, "push", "origin", branch); err != nil {
		t.Fatalf("push init: %v", err)
	}
	cloneB = filepath.Join(base, "b")
	if _, err := runGit(base, "clone", bare, cloneB); err != nil {
		t.Fatalf("clone b: %v", err)
	}
	cfgIdentity(t, cloneB)
	return cloneA, cloneB, branch
}

func TestPullRebaseAutostash_FastForward(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	a, b, branch := setupClones(t)

	// A creates a new commit and pushes it.
	writeFileT(t, a, "f2.txt", "fromA\n")
	runGit(a, "add", "-A")
	runGit(a, "commit", "-m", "a2")
	if _, err := runGit(a, "push", "origin", branch); err != nil {
		t.Fatalf("push a2: %v", err)
	}

	// B runs pull --rebase --autostash: it must receive A's commit.
	if err := PullRebaseAutostash(b, "origin", branch); err != nil {
		t.Fatalf("PullRebaseAutostash: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b, "f2.txt")); err != nil {
		t.Fatalf("f2.txt missing in B after pull: %v", err)
	}
}

func TestPullRebaseAutostash_Conflict(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	a, b, branch := setupClones(t)

	// A edits the same line and pushes.
	writeFileT(t, a, "f.txt", "fromA\n")
	runGit(a, "add", "-A")
	runGit(a, "commit", "-m", "a-edit")
	if _, err := runGit(a, "push", "origin", branch); err != nil {
		t.Fatalf("push a: %v", err)
	}

	// B edits the same line divergently and commits locally.
	writeFileT(t, b, "f.txt", "fromB\n")
	runGit(b, "add", "-A")
	runGit(b, "commit", "-m", "b-edit")

	// pull --rebase must conflict and abort, leaving a clean working tree.
	err := PullRebaseAutostash(b, "origin", branch)
	if !errors.Is(err, ErrRebaseConflict) {
		t.Fatalf("expected ErrRebaseConflict, got: %v", err)
	}
	st, _ := Status(b)
	if strings.TrimSpace(st) != "" {
		t.Fatalf("working tree not clean after abort: %q", st)
	}
}
