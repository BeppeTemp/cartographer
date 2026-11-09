package service

import (
	"fmt"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// The definition files this package writes live in the user's home, so they go
// through internal/safewrite (D148): a symlink planted at the destination or in
// a parent directory is refused with an error naming the path, never written
// through. Removal likewise takes only a regular file.

// mkdirSafe is os.MkdirAll that refuses a symlinked parent.
func mkdirSafe(dir string) error {
	base, err := userHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	return safewrite.MkdirAllNoFollow(base, dir, 0o755)
}

// writeSafe is os.WriteFile (0o644) that refuses a symlinked destination or parent.
func writeSafe(path string, data []byte) error {
	base, err := userHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	return safewrite.WriteFile(base, path, data, 0o644)
}

// removeSafe removes a regular file; a symlink at the path or in a parent is
// refused, and a missing file is os.Remove's not-exist error.
func removeSafe(path string) error {
	base, err := userHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	return safewrite.Remove(base, path)
}
