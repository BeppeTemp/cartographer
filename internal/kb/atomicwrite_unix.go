//go:build unix

package kb

import "os"

// renameReplacing renames over an existing file; POSIX rename is atomic and
// has no sharing violations.
func renameReplacing(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }

// syncDir flushes a directory entry so a rename survives a crash. Best effort:
// some filesystems refuse to sync a directory.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
