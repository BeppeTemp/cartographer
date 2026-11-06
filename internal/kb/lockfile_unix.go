//go:build unix

package kb

import (
	"errors"
	"os"
	"syscall"
)

// openLockFileNoFollow opens path read-write, creating it, and fails on a link.
func openLockFileNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o644)
}

// tryLockFile takes a non-blocking exclusive flock. The retry loop lives in
// AcquireProcessLock so the wait is bounded and its failure can name the holder.
func tryLockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// isLockBusy distinguishes "someone else holds it" from a real failure: only the
// first is worth polling for.
func isLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
