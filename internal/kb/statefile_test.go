package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// linkStateDir makes <root>/.cartographer a symlink to a directory outside the
// KB, as a KB cloned from a remote can carry it.
func linkStateDir(t *testing.T, k *KB) string {
	t.Helper()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(k.Root, ".cartographer")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return outside
}

func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("state leaked outside the KB: %v", ents)
	}
}

func TestStateFiles_LinkedDirIsNeverWritten(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	outside := linkStateDir(t, k)
	if err := k.RegisterConflict(Conflict{ConceptID: "a", Path: "a.md"}); err == nil {
		t.Fatal("RegisterConflict through a linked .cartographer must fail")
	}
	e := UsageEntry{Name: "s", Kind: "skill", Provider: "claude", LastUsed: time.Now(), Count: 1}
	if _, err := k.MergeUsage([]UsageEntry{e}); err == nil {
		t.Fatal("MergeUsage through a linked .cartographer must fail")
	}
	assertEmpty(t, outside)
}

func TestStateFiles_LinkedDirIsNeverRead(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	outside := linkStateDir(t, k)
	if err := os.WriteFile(filepath.Join(outside, "conflicts.json"), []byte(`[{"concept_id":"x"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "usage.json"), []byte(`{"entries":[{"name":"s","kind":"skill"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := k.ListConflicts(); err == nil {
		t.Fatal("ListConflicts must refuse a linked .cartographer")
	}
	if _, err := k.LoadUsage(); err == nil {
		t.Fatal("LoadUsage must refuse a linked .cartographer")
	}
}

func TestStateFiles_NonDirectoryComponentIsRefused(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	if err := os.WriteFile(filepath.Join(k.Root, ".cartographer"), []byte(`[{"concept_id":"x"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	// MkdirAll alone fails on a file, so the read paths are what pin the guard.
	if _, err := k.ListConflicts(); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("ListConflicts must refuse with the guard's error, got %v", err)
	}
	if _, err := k.LoadUsage(); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("LoadUsage must refuse with the guard's error, got %v", err)
	}
	if err := k.checkStatePath(""); err == nil {
		t.Fatal("checkStatePath must refuse a .cartographer that is a file")
	}
}

func TestStateFiles_LinkedFileIsNeverRead(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(k.Root, ".cartographer"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"conflicts.json": `[{"concept_id":"x"}]`,
		"usage.json":     `{"entries":[{"name":"s","kind":"skill"}]}`,
	} {
		target := filepath.Join(outside, name)
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(k.Root, ".cartographer", name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	if _, err := k.ListConflicts(); err == nil {
		t.Fatal("ListConflicts must refuse a linked conflicts.json")
	}
	if _, err := k.LoadUsage(); err == nil {
		t.Fatal("LoadUsage must refuse a linked usage.json")
	}
}

func TestStateFiles_LinkedFileIsRefused(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "victim.json")
	if err := os.WriteFile(target, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(k.Root, ".cartographer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(k.Root, ".cartographer", "conflicts.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := k.RegisterConflict(Conflict{ConceptID: "a", Path: "a.md"}); err == nil {
		t.Fatal("a conflicts.json that is a link must be refused")
	}
	if b, _ := os.ReadFile(target); string(b) != "[]" {
		t.Fatalf("the link target was modified: %q", b)
	}
}
