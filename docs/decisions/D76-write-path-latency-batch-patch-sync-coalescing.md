---
topic: concurrency-git
---

# D76 — Write-path latency: batch patch, sync coalescing, asynchronous push

**Status: implemented (2026-07-10).**

**Context.** Analysis of a real intensive-use session on the `homelab-wiki` KB (33 `concept_patch` on the same concept, median ~13s per call). Aside from the dominant client-side cause (auto-mode classifier without an allowlist for `mcp__cartographer__*`, already fixed upstream via `~/.claude/settings.json`), the server paid for **every** write a synchronous fetch+pull-rebase (`SyncIn`) and push (`SyncOut`) to the remote, even for closely spaced writes; and single-edit `concept_patch` multiplied calls = commits = pushes for an update spanning multiple points of the same concept.

**Decision.**

a) `concept_patch` accepts an optional `edits: [{old_string, new_string, replace_all?}]` field, mutually exclusive with the top-level `old_string`/`new_string`/`replace_all` triple (which remains valid for backward compatibility). **Atomic, sequential** application: each edit sees the body resulting from the previous edit; if any fails (`old_string_not_found`/`old_string_ambiguous`) the whole call fails without writing anything, with the **index** of the offending edit in the error message. A single `writeConceptAndIndex`/commit per call, as for the single form.

b) `tools/list` exposes `annotations.readOnlyHint: true` for the tools marked `[R]`, populated from the existing source of truth `readOnlyToolNames` (`internal/mcpserver/readonly.go`) — MCP clients can thus auto-approve reads without a manual allowlist, obtaining client-side the same effect as the allowlist fix mentioned above.

c) Freshness window on `SyncIn`: within `git.in_window` (env `CARTOGRAPHER_SYNC_IN_WINDOW`, default **30s**) of the last successful fetch+pull, subsequent writes skip `SyncIn` (no-op). `0` = pre-D76 behavior (sync at every write).

d) Asynchronous, per-KB debounced `SyncOut`: `git.out_debounce` (env `CARTOGRAPHER_SYNC_OUT_DEBOUNCE`, default **3s**) takes the push off the MCP response's critical path — after the commit, `gitWrap` signals a pending push to a per-KB worker (lazily started, coalescing: N closely spaced writes = 1 push, executed `out_debounce` after the last signal) instead of calling `SyncOut` inline. `0` = synchronous inline push and no worker started — rollback flag. The worker runs under the same KB `WithGitLock` (no new lock, serialization with writes guaranteed); push conflicts go through `KB.OnPushConflict`, wired to the same conflict registry/`degraded` marker as the synchronous path (D31). Pending pushes are forced (flushed) before the sync-sensitive tools (`sync_check`, `sync_apply`, `sync_pull`, `git_conflict_resolve`) and at server shutdown: stdio on `Run`'s return, HTTP with a new SIGINT/SIGTERM graceful-shutdown handler (previously absent).

e) Per-phase telemetry on stderr for every write, in `gitWrap`: `cartographer: timing op=... sync_in=Xms handler=Xms commit=Xms push=Xms|async total=Xms`.

**Rationale.** The "commit per operation" invariant (D30) stays intact: only the network *transport* (fetch/push) is relaxed, never local traceability — every write still produces exactly one synchronous local commit. The push was already declared non-fatal by design (D30: its failure does not turn a successful write into an error); making it asynchronous is therefore consistent with the existing behavior, not a change of guarantees. Within the `SyncIn` window, a concurrent remote change not seen immediately is not lost: it still surfaces at push time (`SyncOut`'s retries do pull-rebase) and ends up in the conflicts/degraded registry (D31) — the same mechanism already in use on the synchronous path.
