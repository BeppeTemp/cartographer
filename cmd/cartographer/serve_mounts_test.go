package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
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

// AC-cross-kb-links-3
func TestServeWiresMounts(t *testing.T) {
	data := t.TempDir()
	if err := config.WriteServerState(data, config.ServerState{KBAliases: []config.KBAlias{{From: "old-b", To: "kb-b"}}}); err != nil {
		t.Fatal(err)
	}
	a, b := &kb.KB{Root: t.TempDir()}, &kb.KB{Root: t.TempDir()}
	wireMounts([]*kb.KB{a, b}, map[string]*kb.KB{"kb-a": a, "kb-b": b}, []string{"kb-a", "kb-b"}, &config.Config{Data: data})
	for _, k := range []*kb.KB{a, b} {
		m := k.Mounts()
		if m == nil {
			t.Fatal("a mounted KB has no Mounts()")
		}
		if got, ok := m.Lookup("kb-b"); !ok || got != b {
			t.Errorf("sibling kb-b not found")
		}
		if got, ok := m.Lookup("old-b"); !ok || got != b {
			t.Errorf("alias old-b of the server state not followed")
		}
	}
	if a.MountName() != "kb-a" || b.MountName() != "kb-b" {
		t.Errorf("MountName = %q, %q", a.MountName(), b.MountName())
	}
	// No settings: mounts without aliases.
	c := &kb.KB{Root: t.TempDir()}
	wireMounts([]*kb.KB{c}, map[string]*kb.KB{"kb-c": c}, []string{"kb-c"}, nil)
	if _, ok := c.Mounts().Lookup("old-b"); ok || c.MountName() != "kb-c" {
		t.Errorf("nil config: unexpected aliases or name %q", c.MountName())
	}
}
