package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTreeBlobsAndDiffTreeKeepOddPaths(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	mustGit(t, dir, "config", "user.email", "t@example.com")
	mustGit(t, dir, "config", "user.name", "t")
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/a b.md", "one")
	write("docs/è.md", "two")
	write("gone.md", "x")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-qm", "1")
	first, _ := HeadSHA(dir)

	blobs, err := TreeBlobs(dir, "HEAD")
	if err != nil || len(blobs) != 3 {
		t.Fatalf("TreeBlobs = %v, %v", blobs, err)
	}
	got := map[string]bool{}
	for _, b := range blobs {
		got[b.Path] = b.ID != "" && b.Mode == "100644"
	}
	if !got["docs/a b.md"] || !got["docs/è.md"] {
		t.Fatalf("paths mangled: %v", got)
	}

	write("docs/a b.md", "changed")
	write("docs/new é.md", "n")
	if err := os.Remove(filepath.Join(dir, "gone.md")); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-qm", "2")
	ch, err := DiffTree(dir, first, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	st := map[string]TreeChange{}
	for _, c := range ch {
		st[c.Path] = c
	}
	if len(ch) != 3 || st["docs/a b.md"].Status != "M" || st["docs/new é.md"].Status != "A" ||
		st["gone.md"].Status != "D" || st["gone.md"].ID != "" || st["docs/a b.md"].ID == "" {
		t.Fatalf("DiffTree = %+v", ch)
	}
}
