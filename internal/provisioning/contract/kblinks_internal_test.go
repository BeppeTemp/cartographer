package contract

import (
	"os"
	"path/filepath"
	"testing"
)

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
		got, err := FirstKBLink(root, rel)
		if err != nil || got != want {
			t.Errorf("FirstKBLink(%q) = %q, %v; want %q", rel, got, err, want)
		}
	}
}
