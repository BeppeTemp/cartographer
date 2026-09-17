//go:build windows

package provisioning

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// The client-state lock is an empty file nobody reads, so unlike the KB lock
// (internal/kb/lockfile_windows.go, whose offset exists to keep the holder's
// identity readable) this one can take byte zero. What it must not do is
// degrade to a no-op: two clients syncing the same home directory at once is
// exactly what D172 serialises.
const clientLockByteLength uint32 = 1

func clientLockRange() *windows.Overlapped { return &windows.Overlapped{} }

// tryLockFile takes a non-blocking exclusive lock. The retry loop lives in
// LockClientState so the wait is bounded and its failure message can name the
// file.
func tryLockFile(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, clientLockByteLength, 0, clientLockRange(),
	)
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, clientLockByteLength, 0, clientLockRange())
}

// isLockBusy distinguishes "someone else holds it" from a real failure.
func isLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING)
}
