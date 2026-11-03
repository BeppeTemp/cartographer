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
// renamed over path; the rename replaces a link rather than following it. The
// mode of an existing file is kept, perm applies to a new one. When the file
// already holds exactly data nothing is written, so its mtime and inode stay
// (a client's file watcher is not woken at every sync). A failure removes the
// temp file and leaves path as it was.
func WriteFileAtomicNoFollow(baseDir, path string, data []byte, perm os.FileMode) (err error) {
	if err := CheckPath(baseDir, path); err != nil {
		return err
	}
	mode := perm
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode().IsRegular() {
			mode = info.Mode().Perm()
			if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, data) {
				return nil
			}
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".cartographer-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err = syncFile(tmp); err != nil {
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return renameFile(tmpName, path)
}
