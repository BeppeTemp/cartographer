package safewrite

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

// D216 WP7: the D148 guard refuses every destination that is not a plain
// regular file or a plain directory, not only a symlink. os.ModeIrregular is
// the mode a Windows reparse point may be reported as — unverified on a Windows
// host, which is exactly why it is refused rather than reasoned about — and no
// test host can create one on demand, so the stat is stubbed. The widening must
// stay a widening: nothing accepted before may be refused now and nothing
// refused before may be accepted.
func TestIsUnsafeRefusesEveryNonPlainMode(t *testing.T) {
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
		if !IsUnsafe("/anything") {
			t.Errorf("%s (mode %v): accepted, want refused", name, mode)
		}
	}
	for name, mode := range accepted {
		lstat = func(string) (os.FileInfo, error) { return stubFileInfo{mode: mode}, nil }
		if IsUnsafe("/anything") {
			t.Errorf("%s (mode %v): refused, want accepted", name, mode)
		}
	}

	// A path that does not exist is not refused: provisioning creates it.
	lstat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	if IsUnsafe("/not/there") {
		t.Error("a non-existent path was refused, want accepted")
	}
}

// The refusal message must not claim a symlink target it does not have: an
// irregular destination has none to read, and "-> (unreadable)" reads like a
// broken link instead of like the reparse point it may be.
func TestRefusalDescribesANonSymlinkRefusal(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := refusal(plain)
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

func TestCheckPathWalksEveryComponentAndTheFileItself(t *testing.T) {
	skipOnWindows(t)
	base := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(base, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "f"), filepath.Join(base, "real", "file")); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{filepath.Join("linked", "f"), filepath.Join("linked", "new", "f"), filepath.Join("real", "file")} {
		if err := CheckPath(base, filepath.Join(base, rel)); !errors.Is(err, ErrSymlinkDestination) {
			t.Errorf("CheckPath(%s) = %v, want ErrSymlinkDestination", rel, err)
		}
	}
	for _, rel := range []string{"absent", filepath.Join("real", "absent"), filepath.Join("a", "b", "c")} {
		if err := CheckPath(base, filepath.Join(base, rel)); err != nil {
			t.Errorf("CheckPath(%s) = %v, want nil", rel, err)
		}
	}
	// The base dir itself may be a link.
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(base, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckPath(link, filepath.Join(link, "real", "absent")); err != nil {
		t.Errorf("CheckPath under a symlinked base = %v, want nil", err)
	}
}

func TestWriteFileAndRemoveRefuseALinkAndLeaveItsTargetAlone(t *testing.T) {
	skipOnWindows(t)
	base := t.TempDir()
	elsewhere := t.TempDir()
	target := filepath.Join(elsewhere, "target")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(base, "file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(base, "dir")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(base, "file"), filepath.Join(base, "dir", "target")} {
		if err := WriteFile(base, p, []byte("overwritten"), 0o644); !errors.Is(err, ErrSymlinkDestination) {
			t.Errorf("WriteFile(%s) = %v, want ErrSymlinkDestination", p, err)
		}
		if err := Remove(base, p); !errors.Is(err, ErrSymlinkDestination) {
			t.Errorf("Remove(%s) = %v, want ErrSymlinkDestination", p, err)
		}
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "original" {
		t.Errorf("target = %q, %v", b, err)
	}

	plain := filepath.Join(base, "plain")
	if err := WriteFile(base, plain, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile on a plain path = %v", err)
	}
	if err := Remove(base, plain); err != nil {
		t.Fatalf("Remove on a plain path = %v", err)
	}
}

func TestMkdirAllNoFollowRefusesALinkedComponent(t *testing.T) {
	skipOnWindows(t)
	base := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(base, "linked")); err != nil {
		t.Fatal(err)
	}
	err := MkdirAllNoFollow(base, filepath.Join(base, "linked", "sub"), 0o755)
	if !errors.Is(err, ErrSymlinkDestination) {
		t.Fatalf("MkdirAllNoFollow = %v, want ErrSymlinkDestination", err)
	}
	if _, statErr := os.Stat(filepath.Join(elsewhere, "sub")); statErr == nil {
		t.Error("a directory was created through the link")
	}
}
