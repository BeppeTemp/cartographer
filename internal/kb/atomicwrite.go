package kb

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
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
	// CreateTemp makes the file 0600 and the rename would keep it: the file
	// keeps the permission bits of the one it replaces, 0644 for a new one,
	// as files that arrive through git have them.
	if err := tmp.Chmod(destMode(dest)); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writeFileAtomic: chmod: %w", err)
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

// destMode is the permission a rewritten file keeps: those of the regular file
// at dest, 0644 when there is none (or it is not a regular file).
func destMode(dest string) os.FileMode {
	if info, err := os.Lstat(dest); err == nil && info.Mode().IsRegular() {
		return info.Mode().Perm()
	}
	return 0o644
}

// tempNameRe is exactly what os.CreateTemp(dir, tempFilePrefix) produces: the
// prefix and a decimal number. Anything else with the prefix is somebody's file.
var tempNameRe = regexp.MustCompile(`^\.tmp-wiki-[0-9]+$`)

// sweepMaxEntries caps the walk so a huge tree cannot stall the server start.
const sweepMaxEntries = 200000

// SweepStaleTempFiles removes the temporary files of writes that were killed
// before their rename: a regular file named exactly as os.CreateTemp names them,
// older than the lock timeout (a younger one may be a write in progress), and
// not tracked by git. It runs only when the advisory KB lock can be taken
// without waiting, and is meant for the server's own start-up, not for every
// Open (a CLI command opening a KB briefly would hold the lock and walk the
// tree). Best effort and silent: a KB that cannot be swept is still usable.
func (k *KB) SweepStaleTempFiles() {
	release, err := k.AcquireProcessLock(0, "sweep temporary files")
	if err != nil {
		return
	}
	defer release()
	cutoff := time.Now().Add(-processLockTimeout)
	var stale []string
	visited := 0
	_ = filepath.WalkDir(k.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if visited++; visited > sweepMaxEntries {
			return filepath.SkipAll
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".cartographer", ".worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !tempNameRe.MatchString(d.Name()) || !d.Type().IsRegular() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil && info.ModTime().Before(cutoff) {
			stale = append(stale, p)
		}
		return nil
	})
	if len(stale) == 0 {
		return
	}
	rels := make([]string, 0, len(stale))
	for _, p := range stale {
		if rel, rerr := filepath.Rel(k.Root, p); rerr == nil {
			rels = append(rels, filepath.ToSlash(rel))
		}
	}
	tracked, terr := gitx.TrackedPaths(k.Root, rels...)
	if terr != nil && gitx.IsRepo(k.Root) {
		return // cannot tell what is tracked: remove nothing
	}
	for i, p := range stale {
		if i < len(rels) && tracked[rels[i]] {
			continue
		}
		_ = os.Remove(p)
	}
}
