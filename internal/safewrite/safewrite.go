// Package safewrite guards client-side writes against symlinks.
//
// A write, mkdir or remove under a client's home or project directory is
// refused when the file itself, or any directory component below the client
// base dir, is a symlink or any other destination that is not a plain file or
// directory (D148, D216). The package imports nothing from this module, so
// every package that writes there (provisioning, configurator, blocktext) can
// use it.
package safewrite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrSymlinkDestination is returned when a destination — the file itself or any
// directory component under the client base dir — is a symlink, or anything
// else that is not a plain file or a plain directory (see IsUnsafe). The
// sentinel keeps its symlink name because that is what every caller matches
// with errors.Is; the refusal it carries is broader than its name.
//
// os.WriteFile on a symlinked path opens the *target* with O_WRONLY|O_TRUNC: it
// does not replace the link. Symlinked client-config directories are ordinary
// (a dotfile manager, a monorepo checkout, a shared team directory, an earlier
// bootstrap that linked skills out of a source repository), and when the target
// is another git repository the write lands there. os.Remove, os.MkdirAll and
// os.ReadFile behave the same way on a symlinked directory component.
//
// The KB side already guards this (internal/kb/asset.go Lstat's every component
// and refuses to traverse a link); the client side did not, hence D148.
var ErrSymlinkDestination = errors.New("destination is a symlink")

// lstat is os.Lstat behind a test seam: os.ModeIrregular is the mode the guard
// exists for and the one no test host can create on demand.
var lstat = os.Lstat

// refusal builds the operator-facing error, naming the target so it is obvious
// where the write would have landed. A destination that is refused without
// being a symlink (see IsUnsafe) has no target to name, so it gets the shape of
// the refusal instead of a "-> (unreadable)" that reads like a broken link.
func refusal(path string) error {
	if target, err := os.Readlink(path); err == nil {
		return fmt.Errorf("%w: refusing to write through symlink %s -> %s (replace the link with a regular file or directory; a dotfile manager or an earlier bootstrap may have linked it)",
			ErrSymlinkDestination, path, target)
	}
	return fmt.Errorf("%w: refusing to write through %s, which is neither a plain file nor a plain directory (replace it with a regular file or directory; a reparse point, junction, device or socket may be standing in for it)",
		ErrSymlinkDestination, path)
}

// Refusal is refusal for callers that detect an unsafe path themselves.
func Refusal(path string) error { return refusal(path) }

// IsUnsafe reports whether path exists and is something a client-side write
// must not go through: anything that is not a plain regular file or a plain
// directory. A path that does not exist yet is not one — the writer creates it.
//
// It is deliberately broader than the symlink D148 was written against, and only
// ever broader: no path refused before is accepted now. A Windows directory
// *junction* is a reparse point, and whether Go reports one as os.ModeSymlink or
// only as os.ModeIrregular was never verified on a Windows host. Refusing both,
// plus devices, sockets and named pipes, makes the guard correct either way.
//
// A regular file where a directory is expected stays *accepted* here on
// purpose: MkdirAll already fails on it with an accurate error, and reporting it
// as a hostile destination would be a false accusation.
func IsUnsafe(path string) bool {
	info, err := lstat(path)
	if err != nil {
		return false
	}
	mode := info.Mode()
	return !mode.IsRegular() && !mode.IsDir()
}

// EnsureSafeDir walks relDir's components under baseDir and refuses the first
// one IsUnsafe rejects. baseDir itself is deliberately exempt: it may
// legitimately be a link (a symlinked $HOME), so the walk starts below it. A
// component that does not exist yet is fine.
func EnsureSafeDir(baseDir, relDir string) error {
	relDir = filepath.Clean(relDir)
	if relDir == "." || relDir == string(filepath.Separator) {
		return nil
	}
	current := baseDir
	for _, part := range strings.Split(relDir, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if IsUnsafe(current) {
			return refusal(current)
		}
	}
	return nil
}

// CheckPath refuses path when it, or any component between baseDir and it, is
// unsafe. A path outside baseDir is checked as the file itself only, since no
// base anchors a walk for it.
func CheckPath(baseDir, path string) error {
	rel, err := filepath.Rel(baseDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if IsUnsafe(path) {
			return refusal(path)
		}
		return nil
	}
	return EnsureSafeDir(baseDir, rel)
}

// WriteFileNoFollow is os.WriteFile that refuses to follow a symlinked — or
// otherwise non-plain — target. It checks the file only; WriteFile also checks
// the directory chain.
func WriteFileNoFollow(path string, data []byte, perm os.FileMode) error {
	if IsUnsafe(path) {
		return refusal(path)
	}
	return os.WriteFile(path, data, perm)
}

// MkdirAllNoFollow is os.MkdirAll with the component walk in front of it, so a
// symlinked intermediate directory is refused instead of silently traversed.
// baseDir anchors the walk; dir must be inside it.
func MkdirAllNoFollow(baseDir, dir string, perm os.FileMode) error {
	rel, err := filepath.Rel(baseDir, dir)
	if err == nil && !strings.HasPrefix(rel, "..") {
		if err := EnsureSafeDir(baseDir, rel); err != nil {
			return err
		}
	}
	return os.MkdirAll(dir, perm)
}

// WriteFile writes path after checking the file and every directory component
// between baseDir and it.
func WriteFile(baseDir, path string, data []byte, perm os.FileMode) error {
	if err := CheckPath(baseDir, path); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

// Remove removes path after checking the file and every directory component
// between baseDir and it. A path that does not exist is os.Remove's own
// not-exist error and is never a refusal: removing nothing needs no guard, so a
// missing file stays the no-op callers expect even under a linked directory.
func Remove(baseDir, path string) error {
	if _, err := lstat(path); err != nil && errors.Is(err, os.ErrNotExist) {
		return &os.PathError{Op: "remove", Path: path, Err: os.ErrNotExist}
	}
	if err := CheckPath(baseDir, path); err != nil {
		return err
	}
	return os.Remove(path)
}
