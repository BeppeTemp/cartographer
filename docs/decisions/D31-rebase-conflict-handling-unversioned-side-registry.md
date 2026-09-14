---
topic: concurrency-git
---

# D31 — Rebase conflict handling: unversioned side registry + `degraded` marker + guided skill

**Decision.** When `git pull --rebase --autostash` hits a conflict during `SyncIn` or `SyncOut`, Cartographer does NOT lock the KB into a rigid state. Instead:

1. `gitx.PullRebaseAutostash` runs `rebase --abort` and returns a structured `*RebaseConflictError` (conflicting files, `LocalSHA`, `RemoteSHA`, `Remote`, `Branch`). `errors.Is(err, ErrRebaseConflict)` remains `true` for backward compatibility.
2. `gitWrap` in `gitwrap.go` detects the `*RebaseConflictError` via `errors.As`, and for each file calls `kb.RegisterConflict` (persisted in `<root>/.cartographer/conflicts.json`) and `kb.MarkDegraded` (adds `status: degraded` to the frontmatter, uncommitted — best-effort).
3. `.cartographer/` is gitignored (added by `kb.Init` and by `ensureCartographerDir`): the registry is local, unversioned.
4. The `conflicts_list` tool (read-only, not wrapped by `gitWrap`) exposes the registry to the agent.
5. `sync_check` includes the `open_conflicts` field (count) for the SessionStart hook.
6. The bundled `kb-conflict-resolve` skill guides the agent step by step through reconciliation via `concept_read` + `concept_write`.

**Rationale.** A rebase conflict is a normal event in a multi-clone model: locking the KB into a `needs-resolution` state with `Retry-After` (per AD8/old design) was too rigid and required privileged tools. The new approach brings conflicts into the agent's domain as first-class data (`degraded` + registry), without halting operations. Side persistence (outside the git working tree) prevents the registry from showing up in diffs and commits. The `degraded` marker is reversible: the agent reconciles and rewrites without `status: degraded` to close the conflict.
