package safewrite

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func strayTemps(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, ".*.cartographer-tmp-*"))
	return m
}

func TestWriteFileAtomicNoFollow_ReplacesAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomicNoFollow(dir, path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(got) != "new" {
		t.Errorf("content = %q", got)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the existing 0600 kept", info.Mode().Perm())
	}
	if len(strayTemps(t, dir)) != 0 {
		t.Errorf("stray temp: %v", strayTemps(t, dir))
	}
	// A new file takes perm.
	fresh := filepath.Join(dir, "sub.json")
	if err := WriteFileAtomicNoFollow(dir, fresh, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(fresh); runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("new file mode = %v", info.Mode().Perm())
	}
}

// Identical bytes: no write at all, so inode and mtime stay.
func TestWriteFileAtomicNoFollow_UnchangedBytesAreNotWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if err := WriteFileAtomicNoFollow(dir, path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Error("an identical write must leave inode and mtime alone")
	}
}

// A failure on the sync or on the rename leaves the old bytes and no temp file.
func TestWriteFileAtomicNoFollow_FaultsLeaveTheOldFile(t *testing.T) {
	boom := errors.New("injected")
	for name, inject := range map[string]func() func(){
		"sync": func() func() {
			old := syncFile
			syncFile = func(*os.File) error { return boom }
			return func() { syncFile = old }
		},
		"rename": func() func() {
			old := renameFile
			renameFile = func(string, string) error { return boom }
			return func() { renameFile = old }
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			restore := inject()
			defer restore()
			if err := WriteFileAtomicNoFollow(dir, path, []byte("new"), 0o644); !errors.Is(err, boom) {
				t.Fatalf("err = %v, want the injected one", err)
			}
			if got, _ := os.ReadFile(path); string(got) != "old" {
				t.Errorf("content = %q, want the old bytes", got)
			}
			if len(strayTemps(t, dir)) != 0 {
				t.Errorf("stray temp: %v", strayTemps(t, dir))
			}
		})
	}
}

func TestWriteFileAtomicNoFollow_RefusesALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomicNoFollow(dir, link, []byte("x"), 0o644); !errors.Is(err, ErrSymlinkDestination) {
		t.Fatalf("err = %v, want ErrSymlinkDestination", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "keep" {
		t.Errorf("target changed: %q", got)
	}
}
