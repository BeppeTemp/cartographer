package gitx

import "testing"

// The memoised answers follow the repository: a branch switch and a changed
// remote URL are seen on the next call, not served from the memo.
func TestMemoFollowsHEADAndConfig(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "remote", "add", "origin", "https://example.com/a.git")

	if b, _ := Branch(dir); b != "main" {
		t.Fatalf("Branch = %q, want main", b)
	}
	if u, _ := RemoteURL(dir, "origin"); u != "https://example.com/a.git" {
		t.Fatalf("RemoteURL = %q", u)
	}
	mustGit(t, dir, "symbolic-ref", "HEAD", "refs/heads/feature-x")
	mustGit(t, dir, "remote", "set-url", "origin", "https://example.com/b.git")
	if b, _ := Branch(dir); b != "feature-x" {
		t.Fatalf("Branch after switch = %q, want feature-x", b)
	}
	if u, _ := RemoteURL(dir, "origin"); u != "https://example.com/b.git" {
		t.Fatalf("RemoteURL after set-url = %q", u)
	}
	mustGit(t, dir, "remote", "remove", "origin")
	if _, err := RemoteURL(dir, "origin"); err == nil {
		t.Fatal("RemoteURL after remove: want an error")
	}
}
