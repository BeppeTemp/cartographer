package webui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A GIT_DIR (or GIT_WORK_TREE) in the environment, as a git hook leaves it,
// must not decide which repository lists the tracked files: root does.
func TestTrackedFilesIgnoreAHostileGitEnvironment(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	root := filepath.Join(repo, "web")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.ts", "b.ts"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(repo, "init", "-q")
	git(repo, "add", "web")
	// Another, empty repository the environment points at. Set after the setup
	// above so the setup itself is not redirected.
	other := t.TempDir()
	git(other, "init", "-q")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))

	paths, ok := trackedFiles(root)
	if !ok || len(paths) != 2 {
		t.Fatalf("trackedFiles = %v, %v; want the 2 files root's own repository tracks", paths, ok)
	}
}
