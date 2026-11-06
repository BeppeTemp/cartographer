package safewrite

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Seams for fault injection: a full disk and a kill between the truncate and
// the write are the failures the atomic writer exists for, and no test host
// produces them on demand.
var (
	syncFile   = func(f *os.File) error { return f.Sync() }
	renameFile = os.Rename
)

// WriteFileAtomicNoFollow replaces the file at path with data in one step, for
// files the user shares with Cartographer (CLAUDE.md, a client's settings JSON,
// config.toml, the lockfile): os.WriteFile truncates first, so ENOSPC, a crash or
// a SessionStart hook killed by its client's timeout between the truncate and the
// write leaves the user's file empty or partial (#1502).
//
// The file and every directory component between baseDir and it are checked as
// CheckPath does; a destination behind a link is refused. The data goes to a
// temp file in the same directory (created exclusively, O_EXCL), is fsynced, and
// renamed over path; the rename replaces a link rather than following it, except for a link the
// allow set lists, which is resolved and the target written, the link kept. The
// mode of an existing file is kept, perm applies to a new one. When the file
// already holds exactly data nothing is written, so its mtime and inode stay
// (a client's file watcher is not woken at every sync). A failure removes the
// temp file and leaves path as it was.
func WriteFileAtomicNoFollow(baseDir, path string, data []byte, perm os.FileMode) error {
	_, err := WriteFileAtomicNoFollowChanged(baseDir, path, data, perm)
	return err
}

// maxLinkHops bounds the chain behind a listed link.
const maxLinkHops = 32

// resolveLink follows link through a chain of links to the first path that is
// not one (it may not exist: a dangling link is created through). Every link of
// the chain must be listed, else the chain stops there with the usual refusal.
// A relative target is resolved as the kernel does, against the physical
// directory of the link (a listed directory link above it changes what ".."
// means); a hop is matched in its physical spelling against the allow set
// (canonicalized when set, so a user may list the logical path they see).
func resolveLink(link string) (string, error) {
	phys := link
	for range maxLinkHops {
		info, err := os.Lstat(phys)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return phys, nil
		}
		if phys != link && !isAllowedPhys(phys) {
			return "", refusal(phys)
		}
		t, err := os.Readlink(phys)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(t) {
			phys = canonicalPath(t)
			continue
		}
		parent := filepath.Dir(phys)
		if real, evalErr := filepath.EvalSymlinks(parent); evalErr == nil {
			parent = real
		}
		phys = filepath.Join(parent, t)
	}
	return "", fmt.Errorf("%w: symlink %s leads through more than %d links", ErrSymlinkDestination, link, maxLinkHops)
}

// WriteFileAtomicNoFollowChanged is WriteFileAtomicNoFollow that also says
// whether the file was written (false: it already held exactly data).
//
// What it does not keep, unlike an in-place write: it replaces a read-only (0444)
// file that os.WriteFile would have refused, and a rename gives the file a new
// inode, so the owner, ACLs, extended attributes and hard links of the original
// are not carried over; only the permission bits are. It refuses nothing the
// in-place writer accepted.
func WriteFileAtomicNoFollowChanged(baseDir, path string, data []byte, perm os.FileMode) (changed bool, err error) {
	if err := CheckPath(baseDir, path); err != nil {
		return false, err
	}
	// A listed leaf link is followed: the temp file and the rename act on the
	// resolved target, so the link survives (a rename over path would replace it).
	link := path
	if info, lerr := os.Lstat(path); lerr == nil && info.Mode()&os.ModeSymlink != 0 && isAllowed(path) {
		if path, err = resolveLink(path); err != nil {
			return false, err
		}
		defer func() { err = notWritable(baseDir, link, err, true) }()
	} else {
		defer func() { err = notWritable(baseDir, link, err, false) }()
	}
	mode := perm
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() && link != path {
			return false, refusal(link)
		}
		if info.Mode().IsRegular() {
			mode = info.Mode().Perm()
			if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, data) {
				return false, nil
			}
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return false, statErr
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".cartographer-tmp-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return false, fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err = syncFile(tmp); err != nil {
		return false, fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err = tmp.Close(); err != nil {
		return false, err
	}
	if err = os.Chmod(tmpName, mode); err != nil {
		return false, err
	}
	if err = renameFile(tmpName, path); err != nil {
		return false, err
	}
	return true, nil
}
