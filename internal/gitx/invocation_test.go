package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newTestRepo is an initialised repository with one commit and a local identity.
func newTestRepo(t *testing.T) string {
	t.Helper()
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	runGit(dir, "config", "user.email", "test@wiki.local")
	runGit(dir, "config", "user.name", "Wiki Test")
	if err := os.WriteFile(filepath.Join(dir, "seed.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "test: seed", "Wiki Test", "test@wiki.local"); err != nil {
		t.Fatalf("seed Commit: %v", err)
	}
	return dir
}

func commitCount(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-list", "--all", "--count").Output()
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// An inherited GIT_DIR / GIT_WORK_TREE / GIT_INDEX_FILE must not redirect a
// command away from the directory it is given.
func TestRepoPinningEnvIsIgnored(t *testing.T) {
	kb := newTestRepo(t)
	other := newTestRepo(t)
	before := commitCount(t, other)

	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))

	if err := os.WriteFile(filepath.Join(kb, "new.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Commit(kb, "test: in kb", "Wiki Test", "test@wiki.local"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := commitCount(t, other); got != before {
		t.Fatalf("the other repository got a commit: %s -> %s", before, got)
	}
	cmd := exec.Command("git", "-C", kb, "ls-tree", "--name-only", "HEAD", "new.md")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_DIR=") && !strings.HasPrefix(kv, "GIT_WORK_TREE=") && !strings.HasPrefix(kv, "GIT_INDEX_FILE=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "new.md" {
		t.Fatalf("new.md not committed in the KB: %q %v", out, err)
	}
	if env := identEnv(); strings.Contains(strings.Join(env, "\n"), "GIT_INDEX_FILE=") {
		t.Fatal("identEnv keeps GIT_INDEX_FILE")
	}
}

// Paths are file names, never globs: img[1].png must not stage img1.png.
func TestPathsAreLiteral(t *testing.T) {
	dir := newTestRepo(t)
	for _, n := range []string{"img[1].png", "img1.png"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := StatusPaths(dir, "img[1].png")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(st, "img1.png") || !strings.Contains(st, "img[1].png") {
		t.Fatalf("StatusPaths matched by glob: %q", st)
	}
	if err := AddPath(dir, "img[1].png"); err != nil {
		t.Fatal(err)
	}
	staged, _ := exec.Command("git", "-C", dir, "diff", "--cached", "--name-only").Output()
	if strings.Contains(string(staged), "img1.png") {
		t.Fatalf("AddPath staged img1.png: %q", staged)
	}
	if err := ResetPaths(dir, "img[1].png"); err != nil {
		t.Fatal(err)
	}
	if err := CommitPaths(dir, []string{"img[1].png"}, "test: one", "Wiki Test", "test@wiki.local"); err != nil {
		t.Fatal(err)
	}
	tracked, _ := exec.Command("git", "-C", dir, "ls-tree", "-r", "--name-only", "HEAD").Output()
	if strings.Contains(string(tracked), "img1.png") || !strings.Contains(string(tracked), "img[1].png") {
		t.Fatalf("CommitPaths committed by glob: %q", tracked)
	}
	if h, err := FileHistory(dir, "img[1].png", 0); err != nil || len(h) != 1 {
		t.Fatalf("FileHistory = %v, %v; want one revision", h, err)
	}
}

// A ref or remote name that begins with "-" is a name, never an option.
func TestDashRefsAreNames(t *testing.T) {
	dir := newTestRepo(t)
	bad := "--output=" + filepath.Join(dir, "pwned")
	calls := map[string]func() error{
		"Checkout":          func() error { return Checkout(dir, bad) },
		"CheckoutNewBranch": func() error { return CheckoutNewBranch(dir, "b", bad) },
		"RenameBranch":      func() error { return RenameBranch(dir, bad) },
		"ResetHardTo":       func() error { return ResetHardTo(dir, bad) },
		"Push":              func() error { return Push(dir, bad, "main") },
	}
	for name, f := range calls {
		if err := f(); err == nil {
			t.Errorf("%s accepted %q", name, bad)
		}
	}
	if _, err := IsAncestor(dir, bad, "HEAD"); err == nil {
		t.Error("IsAncestor accepted an option-shaped ref")
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Fatal("an option-shaped ref was interpreted as an option")
	}
}
