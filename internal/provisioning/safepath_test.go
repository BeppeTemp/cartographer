package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// D216 WP7: the D148 guard refuses every destination that is not a plain
// regular file or a plain directory, not only a symlink. os.ModeIrregular is
// the mode a Windows reparse point may be reported as — unverified on a Windows
// host, which is exactly why it is refused rather than reasoned about — and no
// test host can create one on demand, so the stat is stubbed. The widening must
// stay a widening: nothing accepted before may be refused now and nothing
// refused before may be accepted.
func TestIsUnsafeDestinationRefusesEveryNonPlainMode(t *testing.T) {
	refused := map[string]os.FileMode{
		"symlink":     os.ModeSymlink,
		"irregular":   os.ModeIrregular,
		"device":      os.ModeDevice,
		"char device": os.ModeDevice | os.ModeCharDevice,
		"named pipe":  os.ModeNamedPipe,
		"socket":      os.ModeSocket,
	}
	accepted := map[string]os.FileMode{
		"regular file": 0o644,
		"directory":    os.ModeDir | 0o755,
		// A regular file where a directory is expected stays accepted: MkdirAll
		// reports that accurately, and calling it a hostile destination would be
		// a false accusation.
		"executable file": 0o755,
	}

	restore := lstat
	defer func() { lstat = restore }()

	for name, mode := range refused {
		lstat = func(string) (os.FileInfo, error) { return stubFileInfo{mode: mode}, nil }
		if !isUnsafeDestination("/anything") {
			t.Errorf("%s (mode %v): accepted, want refused", name, mode)
		}
	}
	for name, mode := range accepted {
		lstat = func(string) (os.FileInfo, error) { return stubFileInfo{mode: mode}, nil }
		if isUnsafeDestination("/anything") {
			t.Errorf("%s (mode %v): refused, want accepted", name, mode)
		}
	}

	// A path that does not exist is not refused: provisioning creates it.
	lstat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	if isUnsafeDestination("/not/there") {
		t.Error("a non-existent path was refused, want accepted")
	}
}

// The refusal message must not claim a symlink target it does not have: an
// irregular destination has none to read, and "-> (unreadable)" reads like a
// broken link instead of like the reparse point it may be.
func TestSymlinkErrorDescribesANonSymlinkRefusal(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := symlinkError(plain)
	if !errors.Is(err, ErrSymlinkDestination) {
		t.Fatalf("symlinkError = %v, want ErrSymlinkDestination", err)
	}
	if strings.Contains(err.Error(), "unreadable") || strings.Contains(err.Error(), "->") {
		t.Errorf("error %q describes a link target it does not have", err)
	}
	if !strings.Contains(err.Error(), plain) {
		t.Errorf("error %q does not name the path", err)
	}
}

// stubFileInfo is the minimum os.FileInfo a mode-only assertion needs.
type stubFileInfo struct {
	mode os.FileMode
}

func (s stubFileInfo) Name() string       { return "stub" }
func (s stubFileInfo) Size() int64        { return 0 }
func (s stubFileInfo) Mode() os.FileMode  { return s.mode }
func (s stubFileInfo) ModTime() time.Time { return time.Time{} }
func (s stubFileInfo) IsDir() bool        { return s.mode.IsDir() }
func (s stubFileInfo) Sys() interface{}   { return nil }
