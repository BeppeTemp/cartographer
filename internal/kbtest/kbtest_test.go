package kbtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestNewGivesIndependentCommittedKBs(t *testing.T) {
	a, b := New(t), New(t)
	if a.Root == b.Root {
		t.Fatalf("both fixtures share %s", a.Root)
	}
	if filepath.Base(a.Root) != DirName {
		t.Errorf("fixture directory %q, want basename %q", a.Root, DirName)
	}

	if err := os.WriteFile(filepath.Join(a.DataRoot(), "only-in-a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.DataRoot(), "only-in-a.md")); !os.IsNotExist(err) {
		t.Errorf("a write in one KB is visible in the other (stat err = %v)", err)
	}

	if got := git(t, b.Root, "status", "--porcelain"); got != "" {
		t.Errorf("fresh fixture is not a clean work tree:\n%s", got)
	}
	if got := git(t, b.Root, "rev-list", "--count", "HEAD"); got != "1" {
		t.Errorf("commit count = %s, want 1", got)
	}
	if got := git(t, b.Root, "symbolic-ref", "--short", "HEAD"); got != gitx.DefaultBranch {
		t.Errorf("HEAD on %q, want %q", got, gitx.DefaultBranch)
	}
}
