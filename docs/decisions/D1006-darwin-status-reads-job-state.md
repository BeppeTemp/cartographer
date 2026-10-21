---
topic: control-plane
---

# D1006 — Darwin `service status` reads the job state, not just its registration

**Decision.** On macOS `Status.Running` is true when `launchctl print` succeeds and the job's top-level `state` is not `not running`; a registered job with no process reads as stopped, so `service status` exits 3 and `upgrade-repair` leaves it alone.

**Why.** `Stop` disables the job and sends SIGTERM but keeps it registered, so registration alone made a stopped service indistinguishable from a running one, and `upgrade-repair` tried to replace a job launchd never relaunches (timeout, exit 2).

**Alternatives rejected.** `launchctl print-disabled`: a second call, and it reports the intent rather than whether a process exists. Parsing `pid =`: absent for a job between spawns just like for a stopped one, so no better than `state`.

**Consequences.** Only the top-level `state =` line is read (nested blocks have their own); an unrecognised output counts as running, so a launchctl format change cannot make a live service look stopped. `test/macos/` asserts exit 3 after `stop` and an `install.sh update` that leaves it stopped.
