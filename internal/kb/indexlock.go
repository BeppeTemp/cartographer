package kb

import (
	"fmt"
	"os"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// ClearStaleIndexLock removes the .git/index.lock a crash, a power loss or a kill of
// the server left in the KB's clone, which would otherwise fail every later commit
// with git's "Unable to create index.lock: File exists". It runs only while this
// process holds the KB's exclusive lock (AcquireProcessLock, without waiting): then no
// git of this server runs in the clone, so a lock found is a leftover. When another
// process holds the KB the lock is not ours to judge: nothing is removed, and one
// line on stderr says that a lock exists and the check was skipped. The
// removal is logged with the file's path and age. It is for the server's own start-up,
// like SweepStaleTempFiles, never for a write: a lock that appears later may belong to a
// git the operator runs, and the write error names it instead (gitx.indexLockHint).
// Best effort: a KB that cannot be cleared is still usable.
func (k *KB) ClearStaleIndexLock() {
	release, err := k.AcquireProcessLock(0, "clear a stale index.lock")
	if err != nil {
		// Not ours to judge, but not silent either: the leftover stays until the
		// next start, and a write meanwhile fails on it.
		if lock := gitx.HasIndexLock(k.Root); lock != "" {
			fmt.Fprintf(os.Stderr, "cartographer: %s: an index.lock exists at %s and the check was skipped because another process holds the KB\n", k.Root, lock)
		}
		return
	}
	defer release()
	path, age, err := gitx.RemoveStaleIndexLock(k.Root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: a git index.lock is left in the KB and could not be removed: %v\n", err)
		return
	}
	if path != "" {
		fmt.Fprintf(os.Stderr, "cartographer: removed %s left by a git that did not finish (age %s): the KB's lock was ours and no git of this server was running\n",
			path, age.Round(time.Second))
	}
}
