//go:build windows

package kb

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

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

// stillActive is STILL_ACTIVE from the Windows headers: the exit code a process
// reports while it is running. golang.org/x/sys/windows does not export it.
const stillActive uint32 = 259

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

// processAlive reports whether pid exists. It is what decides whether a lock is
// stale and may be reclaimed, so every uncertain answer is "alive": reclaiming a
// lock that is genuinely held is the single outcome D155 exists to prevent, while
// honouring a stale one only costs the operator a message naming a dead pid.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// ERROR_INVALID_PARAMETER is how Windows says "no process has that pid".
		// ERROR_ACCESS_DENIED says the opposite: a process is there, it just is
		// not ours — another user's or a service's cartographer holding the lock
		// legitimately. Anything else is unexplained, and an unexplained failure
		// is not evidence of death.
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
