//go:build unix

package provisioning

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes a non-blocking exclusive flock. The retry loop lives in
// LockClientState so the wait is bounded and its failure message can name the
// file.
func tryLockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// isLockBusy distinguishes "someone else holds it" from a real failure.
func isLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

// isOpenDenied is false off Windows: the denied-open message is the Windows
// agent-sandbox case, and an EACCES here keeps the plain open error.
func isOpenDenied(error) bool { return false }

func processIdentity() (string, bool) { return "an unknown account", false }
