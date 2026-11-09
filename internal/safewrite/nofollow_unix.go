//go:build !windows

package safewrite

import (
	"errors"
	"os"
	"syscall"
)

// openNoFollow opens path for writing, creating it with perm, without following
// a link and without truncating. O_NONBLOCK keeps a FIFO swapped in from being
// waited on: with no reader the open fails ENXIO, and the fstat refuses one that
// has a reader. ELOOP is a link at the leaf, dangling or not.
func openNoFollow(path string, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, perm)
	if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENXIO) {
		return nil, errNotPlain
	}
	return f, err
}
