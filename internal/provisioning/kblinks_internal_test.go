package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The single-file hash is a second line behind the manifest's own check: a
// descriptor that became a link between the listing and the hash is refused,
// not read.
func TestContentHashFile_RefusesALink(t *testing.T) {
	skipWithoutSymlinks(t)
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "x.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	var linkErr *kbSymlinkError
	if _, err := contentHashFile(link); !errors.As(err, &linkErr) {
		t.Fatalf("contentHashFile(link) = %v, want a *kbSymlinkError", err)
	}
	real := filepath.Join(dir, "y.json")
	if err := os.WriteFile(real, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := contentHashFile(real); err != nil {
		t.Fatalf("contentHashFile(regular file): %v", err)
	}
}

func TestFirstKBLink_NamesTheFirstLinkedComponent(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "hooks", "lnk")); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{
		"hooks/lnk":          "hooks/lnk",
		"hooks/lnk/run.sh":   "hooks/lnk",
		"hooks/plain/run.sh": "",
		"skills/none":        "",
	} {
		got, err := firstKBLink(root, rel)
		if err != nil || got != want {
			t.Errorf("firstKBLink(%q) = %q, %v; want %q", rel, got, err, want)
		}
	}
}
