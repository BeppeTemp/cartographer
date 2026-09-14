---
topic: sync-provisioning
---

# D172 — Sync ordering, per-provider checkpoints, and a client lock

**Decision.** `runSync` writes nothing before the manifest is fetched and
verified; `materializeForProviders` checkpoints the lockfile after every
provider; every path that mutates client state takes an advisory OS file lock;
and `--dry-run` covers the removals and the `known_kbs` rewrite it used to hide.

**Context.** Three distinct defects, none of which is "the sync is not atomic" in
general — that framing hides which write is actually unprotected.

- **Order.** `runSync` removed and rewrote the providers' MCP entries and saved
  `.cartographer.yaml` *before* `fetchMergedManifest`. A failed `sync_pull`, an
  unverifiable signature or a cross-KB collision ([D171](D171-a-cross-kb-collision-is-an-error-not-an-alphabetical.md)) left those
  writes in place, and the error said nothing about it. The artifact writes were
  already correctly ordered — `ensureBootstrapForProviders` sits after the
  manifest check deliberately, and stays there. The fix moves the configuration
  writes to the same side of that line and makes the failure message say that
  nothing changed, because a user who cannot tell what state the machine is in
  will re-run blind.
- **Checkpoints.** `Apply` ran per provider while the lockfile was written once
  at the end, so a failure on provider N left providers 1..N−1 with files on disk
  and **no lock entry**: unmanaged files that pruning never removes and `doctor`
  cannot see. The lockfile is now written after each provider, and the error
  names the ones already recorded so a rerun is informed. N atomic renames
  instead of one, with at most five providers: a deliberate trade of I/O for
  safety.
- **The guarantee, stated rather than implied.** A failure *between* steps leaves
  a consistent state and a completed provider is always recorded. This does
  **not** make a single `Apply` atomic — the failed provider's own partial files
  are still possible, which is [D178](D178-a-file-dropped-from-an-artifact-is-removed-with-it-not.md)'s subject. Saying so is the point:
  the alternative was a generalized snapshot-and-rollback, and rolling back the
  native configs of four providers is more dangerous code than it removes.
- **The lock is at OS level.** Nothing serialized concurrent syncs, and every
  path did a read-modify-write of the lockfile, so two of them lost each other's
  provider entries, last writer wins. This is not hypothetical: the bootstrap
  hook runs `cartographer sync` at session start, so several agent sessions
  opening at once produce concurrent *processes* — which an in-process mutex
  would not see. `.cartographer-client.lock`, `flock`, released on every exit
  path, with a bounded wait that fails naming the file: a sync that silently
  loses an entry is worse than one that asks to be rerun. A dry run writes
  nothing and takes none.
- **The TUI stops fanning out.** `S` ran one `syncCmd` per provider through
  `tea.Batch`. With the lock in place each goroutine would only queue behind the
  others, buying nothing while making the progress reporting incoherent, so it
  runs them sequentially under one lock and reports a partial failure as such.
- **A plan that hides its removals is not a plan.** `--dry-run` showed the files
  and the MCP entries it would add, but not the ones it would remove nor the
  `known_kbs` rewrite — exactly the destructive half. Both are now printed, and a
  `--client`-restricted plan says so in its header, since read without the
  command line that produced it a partial plan looks complete.

**Consequences.** No interface change. Failure behaviour is more conservative,
and there is one new failure mode: two overlapping syncs, where the second now
reports the lock instead of silently corrupting the first one's state.
