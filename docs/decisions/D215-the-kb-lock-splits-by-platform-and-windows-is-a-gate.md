---
topic: concurrency-git
---

# D215 — The per-KB lock splits by platform, and Windows becomes a CI gate

**Decision.** The advisory per-KB lock of D155 no longer calls `syscall.Flock`
directly: `tryLockFile`, `unlockFile`, `isLockBusy` and `processAlive` are declared
identically in `internal/kb/lockfile_unix.go` and `internal/kb/lockfile_windows.go`,
`golang.org/x/sys` becomes a direct dependency for `windows.LockFileEx` and
`windows.OpenProcess`, and a separate `test-windows` job on `windows-latest` runs
`make gate`. The Windows lock takes a **single byte at a 4 GiB offset**, and
`processAlive` treats every answer it cannot explain — `ERROR_ACCESS_DENIED` above
all — as *alive*.

**Why.** The module did not compile for Windows at all, and every error came from
one file: the KB lock. Two properties of the Windows API forced the shape of the
port rather than a mechanical translation. `LockFileEx` is **mandatory** locking,
so the locked range cannot be read by the contender — and the contender is exactly
who reads the lock file, to name the holder in `LockHeldError`; locking the
metadata would have turned every message into `pid 0`, silently. And `OpenProcess`
reports an existing process it may not touch as `ERROR_ACCESS_DENIED`, which read
as "gone" would reclaim a lock that is genuinely held — the one outcome D155 exists
to prevent. The cost is a second CI leg of several minutes on every PR, and one
more platform whose lock semantics a future change has to keep in mind.

**Alternatives rejected.**

- *A `!unix` no-op, as `internal/provisioning/clientlock_other.go` does* — the
  client lock may degrade to nothing, this one guards the git index: the incident
  D155 records is 617 staged deletions in a corrupted index.
- *Locking the whole file, or offset 0* — mandatory locking makes the holder
  unnameable; the error that tells the operator what to stop would report pid 0.
- *A `runtime.GOOS` branch in the shared file* — it compiles on one platform and
  is checked on neither; build tags make `GOOS=windows go vet ./...` a real gate.
- *A matrix leg on the existing `test` job* — `main`'s branch protection requires
  a check named literally `test`, and a matrix renames it to `test (ubuntu-latest)`,
  un-protecting the branch without saying so.
- *Running the whole CI suite on Windows* — `smoke-http`, `e2e` and `test-install`
  are POSIX `sh` harnesses driving a launchd/systemd install path; porting them is
  a different piece of work from making the code compile.
- *Keeping the pid file as the only Windows serialisation* — it catches the common
  case and nothing else, and the case that corrupted an index was not the common
  one.

**Consequences.** `internal/kb` has a platform pair whose two files must keep the
same helper surface; the tests that assert the holder is nameable and that a dead
pid is reclaimed run on both legs, and it is the contention test that fails if the
byte offset ever moves back into the metadata. A non-contention error from the lock
call now fails immediately instead of polling for the whole timeout — a behaviour
change on Unix too, and the intended one. `make gate` is what Windows runs, so
anything added to the gate must work on both platforms or move out of it. No
Windows artifact is published: distribution, the native service and the client
surface stay open work.
