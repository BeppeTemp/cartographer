package provisioning

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ClientLockFileName is the advisory lock guarding every mutation of this
// machine's client state — the lockfile and .cartographer.yaml. It sits next
// to the lockfile it protects.
const ClientLockFileName = ".cartographer-client.lock"

// DefaultClientLockTimeout bounds how long a caller waits for a concurrent
// operation to finish. Long enough for an ordinary sync to complete, short
// enough that a stuck holder is reported rather than waited on forever.
const DefaultClientLockTimeout = 30 * time.Second

// clientLockPollInterval is how often a blocked acquisition retries. The
// lock is held for the length of a sync, so polling costs nothing.
const clientLockPollInterval = 100 * time.Millisecond

// LockClientState takes an exclusive advisory lock on dir's client lock file
// and returns the release function.
//
// The lock is at OS level, not in-process: the case it exists for is several
// `cartographer sync` PROCESSES running at once — the session-start bootstrap
// hook fires one per agent session — and an in-process mutex would not see
// them. Every path that read-modify-writes the lockfile or .cartographer.yaml
// takes it, because the losing writer of such a race silently drops another
// provider's entry.
//
// A blocked acquisition waits up to timeout and then fails naming the file: a
// sync that quietly loses an entry is worse than one that asks to be rerun.
//
// NOTE: this is an advisory lock between cooperating cartographer processes.
// It does not protect against an editor rewriting the same files, which is
// not a race any lock could arbitrate.
func LockClientState(dir string, timeout time.Duration) (func() error, error) {
	if timeout <= 0 {
		timeout = DefaultClientLockTimeout
	}
	path := filepath.Join(dir, ClientLockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open client lock %s: %w", path, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		err := tryLockFile(f)
		if err == nil {
			return func() error {
				unlockErr := unlockFile(f)
				closeErr := f.Close()
				if unlockErr != nil {
					return unlockErr
				}
				return closeErr
			}, nil
		}
		if !isLockBusy(err) {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("another cartographer operation is holding %s (waited %s): wait for it to finish, or remove the file if no cartographer process is running", path, timeout)
		}
		time.Sleep(clientLockPollInterval)
	}
}
