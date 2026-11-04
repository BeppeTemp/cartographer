package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// A stash round trip keeps the bytes it stashed even where core.autocrlf is
// true, the Git for Windows default: the write gate stashes a write and pops
// it back, and a pop that checked it out with CRLF rewrote every gated write.
func TestStashRoundTripKeepsLFUnderAutocrlf(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test")
	mustGit(t, dir, "config", "core.autocrlf", "true")
	mustGit(t, dir, "commit", "-q", "--allow-empty", "-m", "init")

	const want = "line one\nline two\n"
	path := filepath.Join(dir, "page.md")
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := StashPushAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := StashPop(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Fatalf("after stash round trip = %q, want %q", got, want)
	}
}
