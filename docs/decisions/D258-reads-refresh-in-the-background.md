---
topic: concurrency-git
---

# D258 — Reads refresh in the background, and never wait for the remote

**Decision.** A read-only tool that finds the KB's `git.in_window` expired answers at once from the
local clone and starts `SyncIn` for that KB in the background, under the usual KB lock. The change
it pulls is visible from the next call on. At most one background refresh runs per KB: reads that
arrive while it is in flight are served from the clone and start nothing. A failure is handled off
the request exactly as before: a conflict is registered and its concepts degraded, any other error
is logged, and a failed fetch starts the 60 s read backoff (#348). Writes are unchanged: `gitWrap`
still fetches and rebases before the write, synchronously.

**Why.** The read-side fetch was the whole latency of `sync_pull`: 5–7 s per KB against a
~60 ms manifest build, paid on every `cartographer sync` after a pause, because a pause always
outlasts the 30 s window (#361). The client already treats the server as the source of truth only
as of the pull, and the next session-start sync picks up whatever the previous one missed, so a read
that is one call late costs nothing a client relies on. What it costs: the first read after a quiet
period can miss a change another instance pushed, and a read can now run while the background
rebase is rewriting files. Before this, the same overlap happened whenever a read ran during a
write's `SyncIn`.

**Alternatives rejected.**
- A periodic background fetch per KB: every KB, read or not, costs one fetch per window, and a
  read that lands just after the window expired still waits.
- Exempting only `sync_pull`/`sync_check`: `sync` gets faster but every other read tool keeps
  paying the fetch, and "which reads are fresh" becomes a per-tool rule to remember.
- Making writes asynchronous too: a write must not commit on a base it could not refresh (D237).

**Consequences.** A test that expects a remote change to be visible must wait for the background
refresh (`waitReadRefresh` in `internal/mcpserver`), and a test helper that creates a KB with a
remote waits for it in cleanup, before the temporary directory is removed. The per-KB coalescing
state lives in `mcpserver` (`readRefreshes`), keyed by `*kb.KB`, for the life of the server.
