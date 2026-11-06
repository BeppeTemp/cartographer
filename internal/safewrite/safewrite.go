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
	"sort"
	"strings"
	"sync"
	"syscall"
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
		return fmt.Errorf("%w: refusing to write through symlink %q -> %q (replace the link with a regular file or directory, or list %q under providers.<provider>.allow_symlinks in .cartographer.yaml; a dotfile manager or an earlier bootstrap may have linked it)",
			ErrSymlinkDestination, path, target, path)
	}
	return fmt.Errorf("%w: refusing to write through %q, which is neither a plain file nor a plain directory (replace it with a regular file or directory; a reparse point, junction, device or socket may be standing in for it)",
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
func IsUnsafe(path string) bool { return isUnsafe(path, false) }

// isUnsafe is IsUnsafe with the allow set honored (strict false) or ignored.
func isUnsafe(path string, strict bool) bool {
	info, err := lstat(path)
	if err != nil {
		return false
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 && !strict && isAllowed(path) {
		// A listed link is followed when its target is plain or not there yet.
		ti, serr := os.Stat(path)
		if serr != nil {
			return !errors.Is(serr, os.ErrNotExist)
		}
		return !ti.Mode().IsRegular() && !ti.Mode().IsDir()
	}
	return !mode.IsRegular() && !mode.IsDir()
}

// EnsureSafeDir walks relDir's components under baseDir and refuses the first
// one IsUnsafe rejects. baseDir itself is deliberately exempt: it may
// legitimately be a link (a symlinked $HOME), so the walk starts below it. A
// component that does not exist yet is fine.
func EnsureSafeDir(baseDir, relDir string) error { return ensureSafeDir(baseDir, relDir, false) }

func ensureSafeDir(baseDir, relDir string, strict bool) error {
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
		if isUnsafe(current, strict) {
			return refusal(current)
		}
	}
	return nil
}

// CheckPath refuses path when it, or any component between baseDir and it, is
// unsafe. A path outside baseDir is checked as the file itself only, since no
// base anchors a walk for it.
func CheckPath(baseDir, path string) error { return checkPath(baseDir, path, false) }

// CheckPathStrict is CheckPath that ignores the allow set: for a writer that
// must never go through a link (TrustCodexProject).
func CheckPathStrict(baseDir, path string) error { return checkPath(baseDir, path, true) }

func checkPath(baseDir, path string, strict bool) error {
	rel, err := filepath.Rel(baseDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if isUnsafe(path, strict) {
			return refusal(path)
		}
		return nil
	}
	return ensureSafeDir(baseDir, rel, strict)
}

// WriteFileNoFollow is os.WriteFile that refuses to follow a symlinked — or
// otherwise non-plain — target. It checks the file only; WriteFile also checks
// the directory chain.
func WriteFileNoFollow(path string, data []byte, perm os.FileMode) error {
	if IsUnsafe(path) {
		return refusal(path)
	}
	return notWritable("", path, os.WriteFile(path, data, perm), false)
}

// MkdirAllNoFollow is os.MkdirAll with the component walk in front of it, so a
// symlinked intermediate directory is refused instead of silently traversed.
// baseDir anchors the walk; dir must be inside it.
func MkdirAllNoFollow(baseDir, dir string, perm os.FileMode) error {
	rel, err := filepath.Rel(baseDir, dir)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// A name that merely starts with two dots ("..cache") is inside baseDir.
		if err := EnsureSafeDir(baseDir, rel); err != nil {
			return err
		}
	} else if IsUnsafe(dir) {
		// Outside baseDir no base anchors a walk: check the directory itself,
		// as CheckPath does.
		return refusal(dir)
	}
	return notWritable(baseDir, dir, os.MkdirAll(dir, perm), false)
}

// WriteFile writes path after checking the file and every directory component
// between baseDir and it.
func WriteFile(baseDir, path string, data []byte, perm os.FileMode) error {
	if err := CheckPath(baseDir, path); err != nil {
		return err
	}
	return notWritable(baseDir, path, os.WriteFile(path, data, perm), false)
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
	if info, err := lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 && isAllowed(path) {
		target, _ := os.Readlink(path)
		return fmt.Errorf("%w: %w: %q -> %q is a link you listed, so it is your own wiring: remove it by hand",
			ErrSymlinkDestination, ErrLinkedLeaf, path, target)
	}
	return notWritable(baseDir, path, os.Remove(path), false)
}

// RemoveEmptyDir removes the directory at path if it is empty, and never a
// link: os.Remove would unlink a directory link (listed or not) whatever its
// target holds. A non-empty directory or a missing one is os.Remove's own error.
func RemoveEmptyDir(path string) error {
	if info, err := lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	return os.Remove(path)
}

// ErrLinkedLeaf is wrapped (with ErrSymlinkDestination) by Remove when the path
// is a listed link: the link and its target are never deleted, so a caller that
// wanted the file gone writes the reduced content through the link instead.
var ErrLinkedLeaf = errors.New("destination is a followed symlink")

// Link is a followed symlink and where it points.
type Link struct{ Path, Target string }

var (
	allowMu sync.Mutex
	allowed = map[string]bool{}
	// canonical holds each entry with the symlinks of its parent resolved: the
	// physical spelling a hop of a chain is matched against.
	canonical = map[string]bool{}
)

func cleanAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

// SetAllowedLinks replaces the set of links writes may follow (D148 refined):
// absolute logical paths, the link itself, matched lexically. Empty by default.
func SetAllowedLinks(paths []string) {
	allowMu.Lock()
	defer allowMu.Unlock()
	allowed = map[string]bool{}
	canonical = map[string]bool{}
	for _, p := range paths {
		if p != "" {
			allowed[cleanAbs(p)] = true
			canonical[canonicalPath(p)] = true
		}
	}
}

// AllowedLinks returns the set, sorted by path.
func AllowedLinks() []string {
	allowMu.Lock()
	defer allowMu.Unlock()
	out := make([]string, 0, len(allowed))
	for p := range allowed {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ResetAllowedLinks empties the set (tests).
func ResetAllowedLinks() { SetAllowedLinks(nil) }

// canonicalPath is path with the symlinks of its parent directory resolved (the
// last component is left alone: it is the link itself).
func canonicalPath(path string) string {
	path = cleanAbs(path)
	if real, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(real, filepath.Base(path))
	}
	return path
}

// isAllowedPhys reports whether path, in its physical spelling, is a listed link.
func isAllowedPhys(path string) bool {
	allowMu.Lock()
	defer allowMu.Unlock()
	return len(canonical) > 0 && canonical[canonicalPath(path)]
}

func isAllowed(path string) bool {
	allowMu.Lock()
	defer allowMu.Unlock()
	return len(allowed) > 0 && allowed[cleanAbs(path)]
}

// FollowedLinks lists, shortest path first, the listed links that are links on
// disk and sit on path (the path itself included). baseDir, when set, is exempt
// as everywhere else.
func FollowedLinks(baseDir, path string) []Link {
	var out []Link
	path = cleanAbs(path)
	base := ""
	if baseDir != "" {
		base = cleanAbs(baseDir)
	}
	for _, p := range AllowedLinks() {
		if p == base || (p != path && !strings.HasPrefix(path, p+string(filepath.Separator))) {
			continue
		}
		if info, err := lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			t, _ := os.Readlink(p)
			if !filepath.IsAbs(t) {
				t = filepath.Join(filepath.Dir(p), t)
			}
			out = append(out, Link{Path: p, Target: t})
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].Path) < len(out[j].Path) })
	return out
}

// notWritable turns a permission, read-only or (with missing) not-found failure
// of a write that went through a followed link into a per-artifact refusal
// naming the link and its target; any other error, and a write through no
// followed link, keeps its raw error.
func notWritable(baseDir, path string, err error, missing bool) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrPermission) && !errors.Is(err, syscall.EROFS) && !(missing && errors.Is(err, os.ErrNotExist)) {
		return err
	}
	links := FollowedLinks(baseDir, path)
	if len(links) == 0 {
		return err
	}
	l := links[len(links)-1]
	return fmt.Errorf("%w: the target of symlink %s -> %s is not writable (a read-only store or mount?): %w",
		ErrSymlinkDestination, l.Path, l.Target, err)
}
