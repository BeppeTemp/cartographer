package safewrite

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// swapFor runs swap inside the window between the check and the open, for the
// duration of the test.
func swapFor(t *testing.T, swap func()) {
	t.Helper()
	old := beforeOpen
	t.Cleanup(func() { beforeOpen = old })
	beforeOpen = func(string) { swap() }
}

func linkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
}

func TestWritesRefuseALeafSwappedForALinkAfterTheCheck(t *testing.T) {
	writers := map[string]func(base, path string) error{
		"WriteFileNoFollow": func(_, path string) error { return WriteFileNoFollow(path, []byte("new"), 0o644) },
		"WriteFile":         func(base, path string) error { return WriteFile(base, path, []byte("new"), 0o644) },
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			victim := filepath.Join(dir, "victim")
			if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "leaf")
			if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			swapFor(t, func() {
				_ = os.Remove(path)
				linkOrSkip(t, victim, path)
			})
			err := write(dir, path)
			if !errors.Is(err, ErrSymlinkDestination) {
				t.Fatalf("err = %v, want ErrSymlinkDestination", err)
			}
			if b, _ := os.ReadFile(victim); string(b) != "keep" {
				t.Fatalf("victim = %q, want it untouched", b)
			}
		})
	}
}

func TestWritesRefuseADanglingLinkSwappedInAndCreateNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leaf")
	missing := filepath.Join(dir, "missing")
	swapFor(t, func() { linkOrSkip(t, missing, path) })
	if err := WriteFileNoFollow(path, []byte("new"), 0o644); !errors.Is(err, ErrSymlinkDestination) {
		t.Fatalf("err = %v, want ErrSymlinkDestination", err)
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the dangling target was created: %v", err)
	}
}

func TestWriteKeepsModeOfAnExistingFileAndGivesPermToANewOne(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old")
	if err := os.WriteFile(old, []byte("longer content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileNoFollow(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(old); string(b) != "x" {
		t.Fatalf("content = %q, want it truncated and rewritten", b)
	}
	fresh := filepath.Join(dir, "fresh")
	if err := WriteFileNoFollow(fresh, []byte("y"), 0o640); err != nil {
		t.Fatal(err)
	}
	if skipModeCheck() {
		return
	}
	if fi, _ := os.Stat(old); fi.Mode().Perm() != 0o600 {
		t.Fatalf("existing mode = %v, want 0600 untouched", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(fresh); fi.Mode().Perm() != 0o640&^umask() {
		t.Fatalf("new mode = %v, want perm applied at creation", fi.Mode().Perm())
	}
}
