package mcpserver

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The server keeps local state of a KB in <root>/.cartographer/ (the read-access
// log, the search-miss log, the lint history, the auto-repair log). A KB comes
// from a remote and git stores symlinks, so the directory, the file and any
// temporary beside it may be a link to somewhere else. The state is read and
// written only inside the KB: a link on either refuses the access (the state is
// telemetry, a refusal costs the record and nothing more), and a file is
// replaced through a temporary whose name nobody can have planted (D148).

// checkLocalStatePath refuses path when its directory is a symlink or the file
// itself is anything but a regular file. A missing file or directory passes:
// there is nothing to read, and the writer creates them.
func checkLocalStatePath(path string) error {
	dir := filepath.Dir(path)
	if fi, err := os.Lstat(dir); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink, refusing to use it", dir)
	}
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file, refusing to use it", path)
	}
	return nil
}

// ensureLocalStateDir creates the directory of path when it is missing and
// refuses it when it is a link. MkdirAll never creates through an existing
// link, so the check after it is what decides.
func ensureLocalStateDir(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return checkLocalStatePath(path)
}

// writeLocalStateFile replaces path with data atomically: a temporary created
// with a random name (O_EXCL, so never through a link) beside it, then renamed.
func writeLocalStateFile(path string, data []byte) error {
	if err := ensureLocalStateDir(path); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
