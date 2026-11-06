package kb

import (
	"fmt"
	"io/fs"
	"os"
)

// The server keeps local state of a KB (conflicts.json, usage.json) in
// <root>/.cartographer/. A KB comes from a remote and git stores symlinks, so
// the directory, a file in it or the temporary beside it may be a link to
// somewhere else. State is read and written only inside the KB: a link, or a
// component that is not what it should be, refuses the access.

// checkStatePath refuses the .cartographer directory when it exists and is
// anything but a real directory (a symlink, a regular file), and path, when
// given, when it exists and is anything but a regular file. A missing entry
// passes: there is nothing to read, and the writer creates it.
func (k *KB) checkStatePath(path string) error {
	dir := k.cartographerDirPath()
	if fi, err := os.Lstat(dir); err == nil && (fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir()) {
		return fmt.Errorf("%s is not a real directory, refusing to use it", dir)
	}
	if path == "" {
		return nil
	}
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file, refusing to use it", path)
	}
	return nil
}
