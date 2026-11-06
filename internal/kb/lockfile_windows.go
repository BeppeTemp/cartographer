//go:build windows

package kb

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openLockFileNoFollow has no O_NOFOLLOW to ask for: openLockFile's Lstat
// refusal of a link or reparse point is the guard.
func openLockFileNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
}

// The Windows lock takes a single byte 4 GiB into the lock file, and the offset
// is the point of the exercise.
//
// LockFileEx is *mandatory* locking, unlike flock(2): a byte range locked
// exclusively by one process cannot even be read by another. The lock file is
// also the only record of who holds it — the three short lines of pid, timestamp
// and command line written by writeLockOwner — and readLockOwner is called by the
// *contending* process, after its own lock attempt failed, to fill in the
// LockHeldError that tells the operator what to stop. Locking the metadata region
// would make that Read fail and turn every message into "another cartographer
// process (pid 0)", which is the one thing the error exists not to say. One byte
// past anything the file will ever contain keeps the range exclusive and the
// metadata readable.
const (
	lockByteOffsetLow  uint32 = 0
	lockByteOffsetHigh uint32 = 1
	lockByteLength     uint32 = 1
)

func lockRange() *windows.Overlapped {
	return &windows.Overlapped{Offset: lockByteOffsetLow, OffsetHigh: lockByteOffsetHigh}
}

// tryLockFile takes a non-blocking exclusive lock on the reserved byte. The retry
// loop lives in AcquireProcessLock so the wait is bounded and its failure can name
// the holder.
func tryLockFile(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, lockByteLength, 0, lockRange(),
	)
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockByteLength, 0, lockRange())
}

// isLockBusy distinguishes "someone else holds it" from a real failure: only the
// first is worth polling for.
func isLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING)
}
