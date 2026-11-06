package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LockFileName is the advisory per-KB lock a writer takes for the duration of a
// git-touching critical section. Dot-prefixed so every walker already skips it
// (conceptFiles and listMDFiles skip hidden directories through walkVisibleDirs, and
// discoverKBPaths ignores dotfiles).
const LockFileName = ".cartographer.lock"

// lockPollInterval is how often AcquireProcessLock retries while waiting.
const lockPollInterval = 100 * time.Millisecond

// tryLockFileFn indirects the platform helper (lockfile_unix.go,
// lockfile_windows.go) so a test can inject a failure that is not contention: on
// a POSIX host there is no way to make flock(2) fail on purpose, and the
// fail-fast branch below is exactly what must not regress.
var tryLockFileFn = tryLockFile

// LockHeldError reports that another process holds the KB lock, naming the
// holder so the operator knows what to stop.
type LockHeldError struct {
	Path    string
	PID     int
	Command string
	Since   string
}

func (e *LockHeldError) Error() string {
	holder := e.Command
	if holder == "" {
		holder = "another cartographer process"
	}
	return fmt.Sprintf("KB is locked by %s (pid %d) since %s", holder, e.PID, e.Since)
}

// AcquireProcessLock takes the advisory per-KB lock and returns the release
// function. It exists because the only serialisation before D155 was an
// in-process mutex (WithGitLock), blind to any other process: `cartographer
// import` writes into the same directory the server's sync loop manages, and the
// two interleaving corrupted the git index — 617 staged deletions and 224
// untracked entries for the same paths, with HEAD and the working tree verified
// byte-identical.
//
// The exclusive file lock — flock(2) on Unix, LockFileEx on Windows — is the
// only authority: the kernel drops it when its holder dies, so a killed process
// never blocks a KB, and nothing here ever removes the lock file. The pid, time
// and command written in it only name the holder in a LockHeldError.
func (k *KB) AcquireProcessLock(timeout time.Duration, command string) (release func(), err error) {
	path := filepath.Join(k.Root, LockFileName)
	deadline := time.Now().Add(timeout)

	for {
		f, openErr := openLockFile(path)
		if openErr != nil {
			return nil, fmt.Errorf("kb: open lock %s: %w", path, openErr)
		}
		lockErr := tryLockFileFn(f)
		if lockErr == nil {
			if err := writeLockOwner(f, command); err != nil {
				_ = unlockFile(f)
				f.Close()
				return nil, err
			}
			k.procLockHeld.Store(true)
			return func() {
				k.procLockHeld.Store(false)
				_ = f.Truncate(0)
				_ = unlockFile(f)
				f.Close()
			}, nil
		}
		// Contention is worth waiting for; an I/O failure is not. Polling one for
		// the whole timeout and then returning a LockHeldError names a holder
		// that does not exist and hides the real cause.
		if !isLockBusy(lockErr) {
			f.Close()
			return nil, fmt.Errorf("kb: lock %s: %w", path, lockErr)
		}

		pid, cmd, since := readLockOwner(f)
		f.Close()

		// The kernel lock is busy, so its holder is alive by definition. The
		// recorded pid is only text for the error: it may be a pid from another
		// namespace or user (kill(2) says EPERM), or not yet written by a holder
		// that has just won the flock. Removing the file here would let the retry
		// create a new inode and two processes would hold "the" lock.
		if time.Now().After(deadline) {
			return nil, &LockHeldError{Path: path, PID: pid, Command: cmd, Since: since}
		}
		time.Sleep(lockPollInterval)
	}
}

// openLockFile opens the lock file, creating it. The KB comes from a remote and
// git stores symlinks, so the lock path may be a link to a file outside the KB,
// which the owner record would then truncate and overwrite: a path that exists
// and is not a regular file is refused, and the open itself never follows a
// link where the OS can say so (openLockFileNoFollow), which closes the window
// between the check and the open.
func openLockFile(path string) (*os.File, error) {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file, refusing to use it", path)
	}
	return openLockFileNoFollow(path)
}

func writeLockOwner(f *os.File, command string) error {
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("kb: truncate lock: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("kb: seek lock: %w", err)
	}
	_, err := fmt.Fprintf(f, "%d\n%s\n%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339), command)
	return err
}

func readLockOwner(f *os.File) (pid int, command, since string) {
	if _, err := f.Seek(0, 0); err != nil {
		return 0, "", ""
	}
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	lines := strings.Split(strings.TrimSpace(string(buf[:n])), "\n")
	if len(lines) > 0 {
		pid, _ = strconv.Atoi(strings.TrimSpace(lines[0]))
	}
	if len(lines) > 1 {
		since = strings.TrimSpace(lines[1])
	}
	if len(lines) > 2 {
		command = strings.TrimSpace(lines[2])
	}
	if since == "" {
		since = "an unknown time"
	}
	return pid, command, since
}
