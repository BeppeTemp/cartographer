package kb

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tempFilePrefix names the temporary file writeFileAtomic creates next to its
// destination; tempFilePattern is its JunkPattern, so a file orphaned by a kill
// between CreateTemp and the rename is never committed by the next write.
const (
	tempFilePrefix  = ".tmp-wiki-"
	tempFilePattern = tempFilePrefix + "*"
)

// The file operations writeFileAtomic is built from, as variables only so a
// test can assert their order (a crash-durability sequence cannot be failed on
// CI): flush the data, rename, flush the directory entry.
var (
	syncTempFile = func(f *os.File) error { return f.Sync() }
	renameTemp   = renameReplacing
	syncParent   = syncDir
)

// writeFileAtomic writes data to a temporary file in dest's directory, syncs it,
// renames it over dest and syncs the directory. Without the syncs the rename can
// be persisted before the data: after a crash dest would exist and be empty, and
// the next write would commit it as "external changes" and push it. The
// directory sync is best effort (syncDir), as is its failure here: the data is
// already in place.
func writeFileAtomic(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, tempFilePrefix)
	if err != nil {
		return fmt.Errorf("writeFileAtomic: create temp: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writeFileAtomic: write: %w", err)
	}
	if err := syncTempFile(tmp); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writeFileAtomic: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("writeFileAtomic: close: %w", err)
	}
	if err := renameTemp(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("writeFileAtomic: rename: %w", err)
	}
	_ = syncParent(dir)
	return nil
}

// sweepStaleTempFiles removes the temporary files of writes that were killed
// before their rename. A file younger than the lock timeout may belong to a
// write in progress in another process, so only older ones go, and only when the
// advisory KB lock is free. Best effort and silent: a KB that cannot be swept
// is still usable.
func (k *KB) sweepStaleTempFiles() {
	// A KB whose lock file does not exist has never had a writer: take the lock
	// only when it is there, so that opening a KB creates no file of its own.
	if _, err := os.Stat(filepath.Join(k.Root, LockFileName)); err == nil {
		release, err := k.AcquireProcessLock(0, "sweep temporary files")
		if err != nil {
			return
		}
		defer release()
	}
	cutoff := time.Now().Add(-processLockTimeout)
	_ = filepath.WalkDir(k.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".cartographer", ".worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(d.Name(), tempFilePrefix) {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(p)
		}
		return nil
	})
}
