//go:build windows

package safewrite

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func skipModeCheck() bool { return true }

func umask() os.FileMode { return 0 }

func TestWriteRefusesAReparsePointLeaf(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "leaf")
	if err := os.Symlink(victim, path); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	if _, err := openNoFollow(path, 0o644); !errors.Is(err, errNotPlain) {
		t.Fatalf("openNoFollow err = %v, want errNotPlain", err)
	}
	if err := WriteFileNoFollow(path, []byte("new"), 0o644); !errors.Is(err, ErrSymlinkDestination) {
		t.Fatalf("err = %v, want ErrSymlinkDestination", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("victim = %q, want it untouched", b)
	}
}
