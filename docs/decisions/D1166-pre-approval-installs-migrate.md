---
topic: sync-provisioning
---

# D1166 — The first sync approves what a client already runs, unchanged

**Decision.** The first `sync` of a client configuration that has never run the migration approves, as
`cartographer approve` would, every hook, skill or agent that needs approval (D1141), has no approval recorded
under any hash, and is installed according to the lockfile with the same source and content hash the KB serves
now. It prints one line per artifact. The migration runs once: its marker (`approvals_migrated`) lives in the client
state file, is set by the same run, is never cleared, and is not set by a dry run or by a sync that did not pull
every KB. `cartographer approve --pending [--kb <kb>] [--yes]` lists everything `sync` would report as
`needs_approval` and approves it after one confirmation, for the artifacts the migration does not cover.

**Why.** D1141 froze every hook a client had installed before it, so each upgrade cost one `approve` per artifact with
no bulk path and no recognition that the file was already there. For a hash-identical artifact the approval
changes nothing about what runs: the same bytes already run on this machine, under the same registration. What D1141
guards is the next change, and any change to the content moves the hash, so it asks as before. The cost is that
the lockfile, not the person, vouches for the artifact on that one run.

**Alternatives rejected.**
- Grandfathering with no marker, on every sync (D1141 ruled it out): an `approve revoke` would be undone by the
  next sync, since the lockfile keeps the entry.
- Keeping the marker in `.cartographer.yaml`: a person edits that file and a copy of it between machines would carry
  the marker to a client that never migrated; the state file is machine-written.
- Approving by file name on disk instead of the lock hash: the file may have been edited since, and the lock is the
  record of what the client applied.
- Only the bulk command: every client with a KB hook would still need a manual step after upgrading.

**Consequences.** A future change here must keep: approval only for a hash-identical, lock-recorded artifact; one
run per client state, never re-armed, and a revoke never undone; no approval for an MCP descriptor (it was always gated);
and the printed line per artifact. Deleting the state file re-arms the migration, as it also forgets the connected
clients: it is not disposable (`docs/configurator.md`).
