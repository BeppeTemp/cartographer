//go:build unix

package kb

import (
	"errors"
	"os"
	"syscall"
)

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

// processAlive reports whether pid exists. Signal 0 performs the existence and
// permission check without delivering anything.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
