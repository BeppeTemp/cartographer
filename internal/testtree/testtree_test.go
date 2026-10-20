package testtree

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/execbit"
)

func TestCaptureRestoreRoundTrip(t *testing.T) {
	src := t.TempDir()
	for rel, content := range map[string]string{
		"a.txt":         "alpha",
		"sub/b.txt":     "bravo\x00bin",
		"sub/deep/c.sh": "#!/bin/sh\n",
	} {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(src, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "sub", "deep", "c.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	tree, err := Capture(src)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if err := tree.Restore(dst); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	for rel, want := range map[string]string{
		"a.txt":         "alpha",
		"sub/b.txt":     "bravo\x00bin",
		"sub/deep/c.sh": "#!/bin/sh\n",
	} {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !bytes.Equal(got, []byte(want)) {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if fi, err := os.Stat(filepath.Join(dst, "empty")); err != nil || !fi.IsDir() {
		t.Errorf("empty directory not restored: %v", err)
	}
	if execbit.Supported {
		fi, err := os.Stat(filepath.Join(dst, "sub", "deep", "c.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if !execbit.IsExecutable(fi.Mode()) {
			t.Errorf("execute bit lost: mode %v", fi.Mode())
		}
	}
}

func TestCaptureRejectsSymlink(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "target"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(src, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Capture(src)
	if err == nil {
		t.Fatal("Capture accepted a symlink")
	}
	if want := "link"; !bytes.Contains([]byte(err.Error()), []byte(want)) {
		t.Errorf("error %q does not name the path %q", err, want)
	}
}
