---
topic: sync-provisioning
amends: D60
---

# D2342 — The session-start sync is scoped to its client and gated by a 10-minute stamp

**Context.** The bootstrap hook (D60) ran a full `cartographer sync` at every session start, in any folder, and blocked the agent while it did: about 28 s on macOS when nothing changed, 7.5 to 9 s limited to one client. The sync holds the client lock (D172), so sessions started together queued behind it for up to 30 s each (#2340).

**Decision.** (A) Each client's bootstrap script, on POSIX and on Windows, runs `cartographer sync --client <provider>`: a session start syncs only its own client. (B) A hidden flag `sync --if-older-than <duration>` ends the run with exit 0 when the last successful sync of the same `--client` selection is younger than the duration; the bootstrap script passes `10m`. The check is a stat and a read of a stamp file in the client state directory (`.cartographer-sync-stamp[-<clients>]`, its mtime is the datum), made before the client lock and before any network call; the stamp is written by the sync itself, once its apply left nothing failed, refused or skipped and before it first releases the client lock, and never by a dry run. A sync run by hand and the scheduled trigger carry no flag and are unchanged. A client named by `--client` that is not connected here, with `--if-older-than`, is dropped, and when none remains the run syncs every connected provider (without the flag it stays an error): the script bakes in its provider whether or not it is connected, and `|| true` would hide the error for ever.

**Hardening.** The gate is re-checked once the client lock is taken, and the stamp is written before the lock is first released (the sync releases it early, before its reports), so sessions started together run one sync; a run that synced nothing does not stamp, and the stamp carries the binary version, a stamp of another version being stale. A `--client` run keeps the previous `known_kbs` in what it persists, so the other providers' later syncs still know which stale entries are theirs to remove. The script retries once as a plain scoped sync, and only when the error is `flag provided but not defined: -if-older-than` (a binary older than the script).

**Rationale.** The cost is the pull, verify and diff, and the queue is the lock; both are avoided by not running, which a stamp does for the common case of a second session within minutes, and by narrowing the run for the first. The stamp follows the usage-scan throttle (D1623) but is written after the sync, not before: a failed sync must be retried at the next session start.

**Consequences.** A KB change reaches a session within the window of the last sync, or at the next scheduled one. A selection with a name that is not a plain identifier is never gated. Not decided here: running the sync detached, skipping by the folder's binding.
