# Concurrency and git decisions

Commit boundaries, remote synchronization, conflict recovery and read/write freshness. Current behavior: [`../concurrency.md`](../concurrency.md).

These records explain why choices were made and may describe superseded behavior.
For the supported interface, follow the current-state page linked above.

<a id="d30"></a>
## D30 — Git commit per logical operation (Step 1: local commit)

**Decision.** Every write MCP tool (`concept_write`, `archive_create`, `dossier_create`, `log_append`, `snapshot`, `supersede`, `concept_move`, `conflict_resolve`, `skill_install`) is wrapped by `gitWrap` (in `internal/mcpserver/gitwrap.go`) which:
1. Acquires the per-KB mutex (`KB.mu`), serializing disk write + commit.
2. Calls the original tool.
3. On success (no Go error, `res.IsError=false`) calls `KB.CommitOp(message)`.
4. A failed commit is logged to stderr but does NOT propagate the error to the MCP client.

`KB.AutoCommit` is `false` by default (zero-value) — so existing unit tests keep their behavior. The server enables it via `CARTOGRAPHER_GIT_AUTOCOMMIT` (default `true`). `sync_apply` is not wrapped: it writes into the client's `base_dir`, not into the KB.

**Rationale.** Commit-per-operation is the fundamental requirement for agent traceability: each change must be atomically identifiable in git history. The struct-level `false` default guarantees backward compatibility with tests and deployments that do not use git. The `true` default in the server aligns operational behavior with the documented expectation ("each write produces a git commit"). Step 2 (fetch/push, conflict handling) is out of scope for this iteration.

---

<a id="d31"></a>
## D31 — Rebase conflict handling: unversioned side registry + `degraded` marker + guided skill

**Decision.** When `git pull --rebase --autostash` hits a conflict during `SyncIn` or `SyncOut`, Cartographer does NOT lock the KB into a rigid state. Instead:

1. `gitx.PullRebaseAutostash` runs `rebase --abort` and returns a structured `*RebaseConflictError` (conflicting files, `LocalSHA`, `RemoteSHA`, `Remote`, `Branch`). `errors.Is(err, ErrRebaseConflict)` remains `true` for backward compatibility.
2. `gitWrap` in `gitwrap.go` detects the `*RebaseConflictError` via `errors.As`, and for each file calls `kb.RegisterConflict` (persisted in `<root>/.cartographer/conflicts.json`) and `kb.MarkDegraded` (adds `status: degraded` to the frontmatter, uncommitted — best-effort).
3. `.cartographer/` is gitignored (added by `kb.Init` and by `ensureCartographerDir`): the registry is local, unversioned.
4. The `conflicts_list` tool (read-only, not wrapped by `gitWrap`) exposes the registry to the agent.
5. `sync_check` includes the `open_conflicts` field (count) for the SessionStart hook.
6. The bundled `kb-conflict-resolve` skill guides the agent step by step through reconciliation via `concept_read` + `concept_write`.

**Rationale.** A rebase conflict is a normal event in a multi-clone model: locking the KB into a `needs-resolution` state with `Retry-After` (per AD8/old design) was too rigid and required privileged tools. The new approach brings conflicts into the agent's domain as first-class data (`degraded` + registry), without halting operations. Side persistence (outside the git working tree) prevents the registry from showing up in diffs and commits. The `degraded` marker is reversible: the agent reconciles and rewrites without `status: degraded` to close the conflict.

---

<a id="d33"></a>
## D33 — Git Step 4: record→finalize conflict resolution, per-content merge

**Decision.** The conflict-resolution loop (left open at Step 4 by D31) is closed with the `git_conflict_resolve(concept_id, strategy, [body])` tool and a **two-phase** mechanism in `internal/kb/conflicts.go`:
- **Record** (`RecordResolution`): the choice (`ours`/`theirs`/`edit` + `body`) is persisted in the new `resolution_strategy`/`resolution_body` fields of the `Conflict` in the registry. `RegisterConflict` preserves an already recorded resolution if a subsequent re-detection would reset it to empty.
- **Finalize** (`FinalizeConflicts`, fires when `PendingConflictCount==0`): **a single** git transaction — stash of uncommitted `degraded` markers → `git merge --no-commit --no-ff <RemoteSHA>` → for each conflict, overwrite the file with the resolved content and `git add` → reject if conflicting files remain that are not in the registry → merge commit → best-effort `SyncOut` push → empty the registry + drop the stash. On error: `merge --abort` + `stash pop` (restore).

The tool is **not** wrapped by `gitWrap` (it manages its own `WithGitLock`; the wrapper would re-trigger `SyncIn/SyncOut`, hitting the conflict again). New `gitx` primitives: `ShowFile`, `MergeNoCommitNoFF`, `MergeAbort`, `UnmergedFiles`, `AddPath`, `StashDrop`.

**Rationale.** Two reasons behind the non-obvious choices:
1. **Materialization by content** (`git show <sha>:<path>`) instead of `git checkout --ours/--theirs`: during a rebase the semantics of `--ours`/`--theirs` are **inverted** (ours=remote, theirs=local) — a footgun with data-loss risk. Taking the content directly from the local or remote SHA eliminates the ambiguity entirely.
2. **Separate record→finalize** instead of an interactive rebase held open across MCP calls: no persistent "half-done" merge/rebase state (fragile, crash-recovery at boot would abort and lose the work). Decisions live in the registry (crash-safe); finalize is a single, repeatable atomic transaction. The merge (rather than rebase) preserves both histories and makes the push a fast-forward of the remote. Known limit: if the push is rejected because the remote advanced again, the local convergence stays committed and the push is retried on the next write. The Server profile (working branch + PR) remains future work.

---

<a id="d46"></a>
## D46 — Per-KB git identity (author/committer + SSH): per-KB env wins over the process, default committer = author

**Decision.** Connects the identity fields of `KBSpec`/`GitConfig` (D44) to the actual git commands.
`internal/gitx.runGitEnv(dir, env, args...)` runs git with `env` **winning** over the process on
duplicate keys; `Clone`/`Commit`/`Fetch`/`Push`/`PullRebaseAutostash` take a variadic
`env ...string`. `kb.KB` gains `GitAuthorName`/`GitAuthorEmail`/`GitEnv`, used by `CommitOp`/
`SyncIn`/`SyncOut`/conflict resolution. `gitEnvForKB` assembles `GIT_SSH_COMMAND` and
`GIT_COMMITTER_*` with cascading fallback: spec → global → default (default committer = author).
**Rationale.** The "per-KB env wins over the process" inversion is deliberately opposite to
`setupGitSSH`'s "the environment wins" rule (global SSH fallback): at the per-KB level the identity is an
explicit config choice and must prevail over an inherited process environment that could
carry the wrong identity. The cascading fallback (KB → global → hardcoded) keeps zero-value
= pre-existing behavior on unchanged deployments.
Details: `docs/transport-auth.md`, `docs/concurrency.md` §Git synchronization to the remote.

---

<a id="d76"></a>
## D76 — Write-path latency: batch patch, sync coalescing, asynchronous push

**Status: implemented (2026-07-10).**

**Context.** Analysis of a real intensive-use session on the `homelab-wiki` KB (33 `concept_patch` on the same concept, median ~13s per call). Aside from the dominant client-side cause (auto-mode classifier without an allowlist for `mcp__cartographer__*`, already fixed upstream via `~/.claude/settings.json`), the server paid for **every** write a synchronous fetch+pull-rebase (`SyncIn`) and push (`SyncOut`) to the remote, even for closely spaced writes; and single-edit `concept_patch` multiplied calls = commits = pushes for an update spanning multiple points of the same concept.

**Decision.**

a) `concept_patch` accepts an optional `edits: [{old_string, new_string, replace_all?}]` field, mutually exclusive with the top-level `old_string`/`new_string`/`replace_all` triple (which remains valid for backward compatibility). **Atomic, sequential** application: each edit sees the body resulting from the previous edit; if any fails (`old_string_not_found`/`old_string_ambiguous`) the whole call fails without writing anything, with the **index** of the offending edit in the error message. A single `writeConceptAndIndex`/commit per call, as for the single form.

b) `tools/list` exposes `annotations.readOnlyHint: true` for the tools marked `[R]`, populated from the existing source of truth `readOnlyToolNames` (`internal/mcpserver/readonly.go`) — MCP clients can thus auto-approve reads without a manual allowlist, obtaining client-side the same effect as the allowlist fix mentioned above.

c) Freshness window on `SyncIn`: within `git.in_window` (env `CARTOGRAPHER_SYNC_IN_WINDOW`, default **30s**) of the last successful fetch+pull, subsequent writes skip `SyncIn` (no-op). `0` = pre-D76 behavior (sync at every write).

d) Asynchronous, per-KB debounced `SyncOut`: `git.out_debounce` (env `CARTOGRAPHER_SYNC_OUT_DEBOUNCE`, default **3s**) takes the push off the MCP response's critical path — after the commit, `gitWrap` signals a pending push to a per-KB worker (lazily started, coalescing: N closely spaced writes = 1 push, executed `out_debounce` after the last signal) instead of calling `SyncOut` inline. `0` = synchronous inline push and no worker started — rollback flag. The worker runs under the same KB `WithGitLock` (no new lock, serialization with writes guaranteed); push conflicts go through `KB.OnPushConflict`, wired to the same conflict registry/`degraded` marker as the synchronous path (D31). Pending pushes are forced (flushed) before the sync-sensitive tools (`sync_check`, `sync_apply`, `sync_pull`, `git_conflict_resolve`) and at server shutdown: stdio on `Run`'s return, HTTP with a new SIGINT/SIGTERM graceful-shutdown handler (previously absent).

e) Per-phase telemetry on stderr for every write, in `gitWrap`: `cartographer: timing op=... sync_in=Xms handler=Xms commit=Xms push=Xms|async total=Xms`.

**Rationale.** The "commit per operation" invariant (D30) stays intact: only the network *transport* (fetch/push) is relaxed, never local traceability — every write still produces exactly one synchronous local commit. The push was already declared non-fatal by design (D30: its failure does not turn a successful write into an error); making it asynchronous is therefore consistent with the existing behavior, not a change of guarantees. Within the `SyncIn` window, a concurrent remote change not seen immediately is not lost: it still surfaces at push time (`SyncOut`'s retries do pull-rebase) and ends up in the conflicts/degraded registry (D31) — the same mechanism already in use on the synchronous path.

---

<a id="d93"></a>
## D93 — Read-path Git freshness

**Status: implemented (2026-07-24).**

**Decision.** Every read-only MCP tool piggybacks `SyncIn` before serving a Git-synchronised KB. The existing `git.in_window` / `CARTOGRAPHER_SYNC_IN_WINDOW` freshness window limits both reads and writes to at most one fetch per KB per window. A pull that moves HEAD uses D90's reconciliation callback before the handler runs. A read-side rebase conflict is registered and marked degraded, while other sync failures are logged; either failure still serves the current local tree.

**Rationale.** Piggybacking avoids a per-KB background poller and its lifecycle/conflict-context complexity while bounding remote work with the existing window. Reads must stay available during a remote or rebase failure, so stale/degraded local content is preferable to turning an otherwise valid read into an MCP error.

---

<a id="d94"></a>
## D94 — Git-history changes digest

**Status: implemented (2026-07-24).**

**Decision.** `changes_since` is a read-only, server-side digest over Git history. The caller supplies an RFC3339 timestamp or `<N>d`/`<N>h` duration; no per-client last-seen state is stored. File changes aggregate by concept with newest-status-wins, including renames under their destination ID. `.cartographer/` is ignored, while logs, maps and provisioning artifacts remain only in the `other_changes` count.

**Rationale.** Every MCP write already creates a Git commit, so Git supplies the authoritative timestamp, author and concept-level history without introducing client state. A compact per-concept aggregate answers a return-to-work digest without exposing raw commit noise.
