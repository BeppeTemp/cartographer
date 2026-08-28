package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// LockFileName is the advisory per-KB lock a writer takes for the duration of a
// git-touching critical section. Dot-prefixed so every walker already skips it
// (listMDFiles, walkConceptPaths and discoverKBPaths all ignore dotfiles).
const LockFileName = ".cartographer.lock"

// lockPollInterval is how often AcquireProcessLock retries while waiting.
const lockPollInterval = 100 * time.Millisecond

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
// Advisory, not mandatory: a stale lock from a killed process must never
// permanently block a KB, so a lock whose recorded pid is gone is reclaimed with
// a note on stderr. flock(2) where the filesystem supports it; the pid file alone
// is the fallback, which is weaker but still catches the common case.
func (k *KB) AcquireProcessLock(timeout time.Duration, command string) (release func(), err error) {
	path := filepath.Join(k.Root, LockFileName)
	deadline := time.Now().Add(timeout)

	for {
		f, openErr := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
		if openErr != nil {
			return nil, fmt.Errorf("kb: open lock %s: %w", path, openErr)
		}
		if flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr == nil {
			if err := writeLockOwner(f, command); err != nil {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
				return nil, err
			}
			return func() {
				_ = f.Truncate(0)
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}

		pid, cmd, since := readLockOwner(f)
		f.Close()

		// A recorded pid that no longer exists is a crash, not contention.
		if pid > 0 && !processAlive(pid) {
			fmt.Fprintf(os.Stderr, "cartographer: reclaiming stale KB lock from pid %d (%s)\n", pid, cmd)
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, &LockHeldError{Path: path, PID: pid, Command: cmd, Since: since}
		}
		time.Sleep(lockPollInterval)
	}
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

// processAlive reports whether pid exists. Signal 0 performs the existence and
// permission check without delivering anything.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
