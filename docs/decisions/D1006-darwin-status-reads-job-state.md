---
topic: control-plane
---

# D1006 — Darwin `service status` reads the job state, not just its registration

**Decision.** On macOS `Status.Running` is true when `launchctl print` succeeds, the job's top-level `state` is not `not running` and `launchctl print-disabled` does not list the label as disabled (the mark `Stop` leaves); a registered job in either condition reads as stopped unless it has a live `pid` (a job disabled by hand still serves a binary to replace), so `service status` exits 3 and `upgrade-repair` leaves it alone.

**Why.** `Stop` disables the job and sends SIGTERM but keeps it registered, so registration alone made a stopped service indistinguishable from a running one, and `upgrade-repair` tried to replace a job launchd never relaunches (timeout, exit 2).

**Alternatives rejected.** `state` alone: CI showed a job stopped right after a restart still reads `spawn scheduled` (KeepAlive throttle), so the disabled flag is needed. `print-disabled` alone: a job that exited without being disabled would read as running. Parsing `pid =`: absent between spawns just like when stopped.

**Consequences.** Only the top-level `state =` line is read (nested blocks have their own); an unrecognised output of either command counts as running, so a launchctl format change cannot make a live service look stopped. `test/macos/` asserts exit 3 after `stop` and an `install.sh update` that leaves it stopped.

`connect` offers a fresh service install only when none is installed, so a deliberately stopped service is not reinstalled over.
