package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitx recognises outcomes by git's English text; a git built with gettext
// prints it in the user's locale unless LC_ALL=C wins over every other
// assignment. exec keeps the last value of a duplicated key.
func TestGitEnvForcesTheCLocaleLast(t *testing.T) {
	t.Setenv("LC_ALL", "it_IT.UTF-8")
	env := gitEnv([]string{"LC_ALL=fr_FR.UTF-8", "GIT_COMMITTER_NAME=x"})
	if got := env[len(env)-1]; got != "LC_ALL=C" {
		t.Fatalf("last entry = %q, want LC_ALL=C", got)
	}
}

// The behaviour gitEnv protects. It bites only where git ships translations
// and the locale exists (Homebrew git on a non-English macOS); elsewhere git
// falls back to English and the test passes either way.
func TestCommitRecognisesNothingToCommitUnderATranslatedLocale(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("LANGUAGE", "it")
	t.Setenv("LC_ALL", "it_IT.UTF-8")
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "initial", "Wiki Test", "test@wiki.local"); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	if err := Commit(dir, "empty", "Wiki Test", "test@wiki.local"); err != ErrNothingToCommit {
		t.Fatalf("Commit without changes = %v, want ErrNothingToCommit", err)
	}
}
