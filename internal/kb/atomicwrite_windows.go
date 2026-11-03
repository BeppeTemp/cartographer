//go:build windows

package kb

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// renameReplacing renames over an existing file, retrying with a short backoff
// while a concurrent reader (the graph cache validation, the search reconciler,
// Atlas) holds the destination open without FILE_SHARE_DELETE: Windows then
// answers ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION until the reader
// closes. Retrying in the writer covers every reader. Any other error, or the
// last attempt's, is returned.
func renameReplacing(oldPath, newPath string) error {
	delay := 5 * time.Millisecond
	var err error
	for attempt := 0; attempt < 12; attempt++ {
		if err = os.Rename(oldPath, newPath); err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		time.Sleep(delay)
		if delay < 200*time.Millisecond {
			delay *= 2
		}
	}
	return err
}

// syncDir is a no-op on Windows: a directory cannot be opened for sync there,
// and NTFS journals the rename.
func syncDir(string) error { return nil }
