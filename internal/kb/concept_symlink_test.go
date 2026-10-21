package kb

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Concept paths never follow a symlink (D1025): a poisoned remote can commit
// one, and a read would disclose its target while a write would modify it.

const symlinkOutside = "---\ntype: Note\n---\noutside\n"

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func assertConceptOpsRefused(t *testing.T, k *KB, id okf.ConceptID, outside string) {
	t.Helper()
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: T")
	ops := map[string]func() error{
		"read":   func() error { _, err := k.ReadConcept(id); return err },
		"write":  func() error { _, err := k.WriteConcept(id, fm, "x\n", ""); return err },
		"delete": func() error { return k.DeleteConcept(id) },
		"locate": func() error { _, err := k.LocateConcept(id); return err },
	}
	for name, op := range ops {
		if err := op(); !errors.Is(err, okf.ErrInvalidPath) {
			t.Fatalf("%s %s: want ErrInvalidPath, got %v", name, id, err)
		}
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != symlinkOutside {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
}

func TestConceptPathsRefuseASymlinkedMap(t *testing.T) {
	t.Parallel()
	k, _ := Init(tempKB(t))
	outDir := t.TempDir()
	outside := filepath.Join(outDir, "c.md")
	if err := os.WriteFile(outside, []byte(symlinkOutside), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outDir, filepath.Join(k.DataRoot(), "linked"))
	assertConceptOpsRefused(t, k, "linked/c", outside)
}

func TestConceptPathsRefuseASymlinkedFile(t *testing.T) {
	t.Parallel()
	k, _ := Init(tempKB(t))
	outside := filepath.Join(t.TempDir(), "c.md")
	if err := os.WriteFile(outside, []byte(symlinkOutside), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), "m"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, filepath.Join(k.DataRoot(), "m", "c.md"))
	assertConceptOpsRefused(t, k, "m/c", outside)
}

func TestConceptPathsRefuseASymlinkedExpandedIndex(t *testing.T) {
	t.Parallel()
	k, _ := Init(tempKB(t))
	outside := filepath.Join(t.TempDir(), "c.md")
	if err := os.WriteFile(outside, []byte(symlinkOutside), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), "m", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, filepath.Join(k.DataRoot(), "m", "c", "index.md"))
	assertConceptOpsRefused(t, k, "m/c", outside)

	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: T")
	if _, err := k.WriteExpandedConcept("m/c", fm, "x\n", ""); !errors.Is(err, okf.ErrInvalidPath) {
		t.Fatalf("forced expanded write: want ErrInvalidPath, got %v", err)
	}
}

func TestWriteConceptRefusesADanglingSymlink(t *testing.T) {
	t.Parallel()
	k, _ := Init(tempKB(t))
	target := filepath.Join(t.TempDir(), "missing.md")
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), "m"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, target, filepath.Join(k.DataRoot(), "m", "c.md"))
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: T")
	if _, err := k.WriteConcept("m/c", fm, "x\n", ""); !errors.Is(err, okf.ErrInvalidPath) {
		t.Fatalf("want ErrInvalidPath, got %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("the dangling target was created: %v", err)
	}
}

func TestWalkConceptsSkipsASymlinkedFile(t *testing.T) {
	t.Parallel()
	k, _ := Init(tempKB(t))
	outside := filepath.Join(t.TempDir(), "c.md")
	if err := os.WriteFile(outside, []byte(symlinkOutside), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), "m"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, filepath.Join(k.DataRoot(), "m", "c.md"))
	_ = k.WalkConcepts(func(id okf.ConceptID, _ string) error {
		t.Fatalf("walk emitted %s", id)
		return nil
	})
}

func TestExpandConceptRefusesSymlinks(t *testing.T) {
	t.Parallel()
	k, _ := Init(tempKB(t))
	outDir := t.TempDir()
	outside := filepath.Join(outDir, "c.md")
	if err := os.WriteFile(outside, []byte(symlinkOutside), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlinked concept file.
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), "m"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, filepath.Join(k.DataRoot(), "m", "c.md"))
	if err := k.ExpandConcept("m/c"); !errors.Is(err, okf.ErrInvalidPath) {
		t.Fatalf("symlinked file: want ErrInvalidPath, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "m", "c")); err == nil {
		t.Fatal("a directory was created next to the link")
	}
	// A symlinked map directory.
	symlinkOrSkip(t, outDir, filepath.Join(k.DataRoot(), "linked"))
	if err := k.ExpandConcept("linked/c"); !errors.Is(err, okf.ErrInvalidPath) {
		t.Fatalf("symlinked map: want ErrInvalidPath, got %v", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != symlinkOutside {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "c")); err == nil {
		t.Fatal("a directory was created outside the KB")
	}
}
