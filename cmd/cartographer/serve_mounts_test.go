package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStdioMountsError(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "kb-a"), filepath.Join(root, "kb-b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := stdioMountsError([]kbMount{{Path: a, Name: "kb-a"}}); err != nil {
		t.Errorf("one mount: %v", err)
	}
	err := stdioMountsError([]kbMount{{Path: a, Name: "kb-a"}, {Path: b, Name: "kb-b"}})
	if err == nil || !strings.Contains(err.Error(), "kb-a") || !strings.Contains(err.Error(), "kb-b") {
		t.Errorf("two mounts: err = %v, want one naming both KBs", err)
	}
	// Two spellings of one directory are one mount, not a refusal.
	if err := stdioMountsError([]kbMount{{Path: a, Name: "kb-a"}, {Path: a + "/../kb-a", Name: "alias"}}); err != nil {
		t.Errorf("one directory spelled twice: %v", err)
	}
}

func TestMountRootKeyUnifiesSpellings(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "kb-a")
	if err := os.MkdirAll(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := mountRootKey(a+"/../kb-a"), mountRootKey(a); got != want {
		t.Errorf("dot-dot spelling = %q, want %q", got, want)
	}
	if runtime.GOOS == "windows" {
		return // symlinks need privileges there
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(a, link); err != nil {
		t.Fatal(err)
	}
	if got, want := mountRootKey(link), mountRootKey(a); got != want {
		t.Errorf("symlink = %q, want %q", got, want)
	}
}
