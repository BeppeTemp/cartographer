package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
}

func TestEnsureSafeDir(t *testing.T) {
	skipOnWindows(t)

	t.Run("plain nested path is accepted", func(t *testing.T) {
		base := t.TempDir()
		if err := os.MkdirAll(filepath.Join(base, "a", "b"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := ensureSafeDir(base, filepath.Join("a", "b", "c")); err != nil {
			t.Errorf("ensureSafeDir = %v, want nil", err)
		}
	})

	t.Run("symlinked intermediate directory is refused", func(t *testing.T) {
		base := t.TempDir()
		elsewhere := t.TempDir()
		if err := os.Symlink(elsewhere, filepath.Join(base, "skills")); err != nil {
			t.Fatal(err)
		}
		err := ensureSafeDir(base, filepath.Join("skills", "kb-import"))
		if !errors.Is(err, ErrSymlinkDestination) {
			t.Errorf("ensureSafeDir = %v, want ErrSymlinkDestination", err)
		}
		if err != nil && !strings.Contains(err.Error(), elsewhere) {
			t.Errorf("error %q does not name the target %q", err, elsewhere)
		}
	})

	t.Run("base dir itself may be a symlink", func(t *testing.T) {
		real := t.TempDir()
		link := filepath.Join(t.TempDir(), "home")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		if err := ensureSafeDir(link, "skills"); err != nil {
			t.Errorf("ensureSafeDir on a symlinked base = %v, want nil", err)
		}
	})

	t.Run("empty and dot relative paths are accepted", func(t *testing.T) {
		base := t.TempDir()
		for _, rel := range []string{"", "."} {
			if err := ensureSafeDir(base, rel); err != nil {
				t.Errorf("ensureSafeDir(%q) = %v, want nil", rel, err)
			}
		}
	})
}

func TestWriteFileNoFollow(t *testing.T) {
	skipOnWindows(t)

	t.Run("writes a regular file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "f.txt")
		if err := writeFileNoFollow(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("writeFileNoFollow = %v", err)
		}
		if b, err := os.ReadFile(p); err != nil || string(b) != "x" {
			t.Errorf("content = %q, %v", b, err)
		}
	})

	t.Run("refuses a symlinked file and leaves the target untouched", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.txt")
		if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link.txt")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if err := writeFileNoFollow(link, []byte("overwritten"), 0o644); !errors.Is(err, ErrSymlinkDestination) {
			t.Fatalf("writeFileNoFollow = %v, want ErrSymlinkDestination", err)
		}
		b, err := os.ReadFile(target)
		if err != nil || string(b) != "original" {
			t.Errorf("target content = %q, %v — the write followed the link", b, err)
		}
	})
}
