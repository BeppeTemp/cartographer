//go:build windows

package audit

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// The SingleWriter lock is an empty file nobody reads, so it can take byte
// zero, like the client-state lock (internal/provisioning/clientlock_windows.go).
const writerLockByteLength uint32 = 1

func writerLockRange() *windows.Overlapped { return &windows.Overlapped{} }

// tryLockFile takes a non-blocking exclusive lock on the SingleWriter lock.
func tryLockFile(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, writerLockByteLength, 0, writerLockRange(),
	)
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, writerLockByteLength, 0, writerLockRange())
}

// isLockBusy distinguishes "another process writes the log" from a real failure.
func isLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING)
}
