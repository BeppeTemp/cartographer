---
topic: concurrency-git
---

# D33 — Git Step 4: record→finalize conflict resolution, per-content merge

**Decision.** The conflict-resolution loop (left open at Step 4 by D31) is closed with the `git_conflict_resolve(concept_id, strategy, [body])` tool and a **two-phase** mechanism in `internal/kb/conflicts.go`:
- **Record** (`RecordResolution`): the choice (`ours`/`theirs`/`edit` + `body`) is persisted in the new `resolution_strategy`/`resolution_body` fields of the `Conflict` in the registry. `RegisterConflict` preserves an already recorded resolution if a subsequent re-detection would reset it to empty.
- **Finalize** (`FinalizeConflicts`, fires when `PendingConflictCount==0`): **a single** git transaction — stash of uncommitted `degraded` markers → `git merge --no-commit --no-ff <RemoteSHA>` → for each conflict, overwrite the file with the resolved content and `git add` → reject if conflicting files remain that are not in the registry → merge commit → best-effort `SyncOut` push → empty the registry + drop the stash. On error: `merge --abort` + `stash pop` (restore).

The tool is **not** wrapped by `gitWrap` (it manages its own `WithGitLock`; the wrapper would re-trigger `SyncIn/SyncOut`, hitting the conflict again). New `gitx` primitives: `ShowFile`, `MergeNoCommitNoFF`, `MergeAbort`, `UnmergedFiles`, `AddPath`, `StashDrop`.

**Rationale.** Two reasons behind the non-obvious choices:
1. **Materialization by content** (`git show <sha>:<path>`) instead of `git checkout --ours/--theirs`: during a rebase the semantics of `--ours`/`--theirs` are **inverted** (ours=remote, theirs=local) — a footgun with data-loss risk. Taking the content directly from the local or remote SHA eliminates the ambiguity entirely.
2. **Separate record→finalize** instead of an interactive rebase held open across MCP calls: no persistent "half-done" merge/rebase state (fragile, crash-recovery at boot would abort and lose the work). Decisions live in the registry (crash-safe); finalize is a single, repeatable atomic transaction. The merge (rather than rebase) preserves both histories and makes the push a fast-forward of the remote. Known limit: if the push is rejected because the remote advanced again, the local convergence stays committed and the push is retried on the next write. The Server profile (working branch + PR) remains future work.
