---
topic: concurrency-git
---

# D155 — Surviving an external process writing into a synced KB

**Status: implemented (2026-08-28).** Amends [D76](D76-write-path-latency-batch-patch-sync-coalescing.md). Closes #176.

**Context.** `cartographer import` is a separate process writing into the same directory the server's
sync loop manages, and nothing coordinated the two. The only serialisation was `WithGitLock`, a
`sync.Mutex` on the `KB` struct: it serialises concurrent tool calls **in one process** and is blind
to any other. Three failures were observed on one migration.

1. **The git index was corrupted.** After a large import, `git status --porcelain` reported 617
   staged deletions and 224 untracked entries **for the same paths**, with `HEAD` and the working tree
   verified byte-identical in both directions. Recovered with a mixed `git reset`. The mechanism is an
   autostash/rebase interleaving with a foreign `git add`: git's index is process-global state and both
   writers assumed exclusive use of it.
2. **An aborted rebase froze the KB.** An interrupted `pull --rebase --autostash` left a
   `.git/rebase-merge` containing **only** an autostash — no `head-name`, no todo. From then on every
   MCP write failed with git's own *"there is already a rebase-merge directory"*, and the KB stayed
   frozen until the directory was removed by hand. `PullRebaseAutostash` does abort on the failures it
   recognises, but a process killed mid-rebase leaves the directory behind and nothing looked for it.
3. **The search index was stale and nothing said so.** `import` writes through the KB write path but
   from another process, so the server's FTS index was untouched: `search` returned zero results while
   `concept_list` saw everything. `reindex` fixes it but sits in the advanced visibility profile, so it
   is not among the tools an agent session sees. The natural conclusion was that the import had failed.

**Decision.**

- **An advisory on-disk lock**, `.cartographer.lock` in the KB root, taken by the server around its
  git critical section and by `import` for its whole run. Dot-prefixed, so every existing walker
  already skips it, and added to `.git/info/exclude` in **both** `Init` and `Open` — a lock file that
  shows in `git status` would block a server-profile PR reconciliation, which is how the first
  implementation announced itself.
- **Advisory, not mandatory**, deliberately: a stale lock from a killed process must never
  permanently block a KB, so a lock whose recorded pid is gone is reclaimed with a note. `flock(2)`
  where the filesystem supports it, the pid file alone as the weaker fallback.
- **Asymmetric waiting.** The server waits a bounded window and then proceeds under its mutex alone,
  reporting the condition — a foreign lock must not stop the server from writing. `import` **fails
  fast**, naming the holder and the stop/start sequence: an import is an operator action at a
  keyboard, and a silent block is worse than an error. `--dry-run` returns before the KB is opened and
  never contends.
- **Orphan-rebase recovery is detection plus instruction, never automatic deletion.**
  `.git/rebase-merge/` may hold a real operator rebase, or an autostash holding the only copy of
  uncommitted work. `SyncIn`/`SyncOut` refuse first and return an error naming the remedy —
  `rebase --continue`/`--abort`, or the `stash list` inspection then removing the directory. Deleting
  another writer's state automatically is the class of action that caused the incident.
- **`import` tells the operator to reindex**, and rebuilds the index itself when a configured server
  is reachable. Printing the instruction is the mandatory half; the automatic call is convenience and
  degrades to the instruction on any failure, never changing the exit code — the import succeeded.
- **Deliberately not done: a marker file the server watches.** It adds a background code path and a
  new failure mode to save one command, and the server already reconciles derived indexes after a sync
  that changed `HEAD`; the gap is only for writes that bypass the server entirely, which an explicit
  instruction covers.

**Consequences.** Every KB root gains a gitignored `.cartographer.lock`, and `import` can now refuse
to start. **This does not make concurrent writing safe** — it makes it *detected*: the server remains
the single writer for MCP traffic, and `WithGitLock`'s in-process semantics are unchanged.
