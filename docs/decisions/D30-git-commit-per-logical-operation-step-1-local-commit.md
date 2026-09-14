---
topic: concurrency-git
---

# D30 — Git commit per logical operation (Step 1: local commit)

**Decision.** Every write MCP tool (`concept_write`, `archive_create`, `dossier_create`, `log_append`, `snapshot`, `supersede`, `concept_move`, `conflict_resolve`, `skill_install`) is wrapped by `gitWrap` (in `internal/mcpserver/gitwrap.go`) which:
1. Acquires the per-KB mutex (`KB.mu`), serializing disk write + commit.
2. Calls the original tool.
3. On success (no Go error, `res.IsError=false`) calls `KB.CommitOp(message)`.
4. A failed commit is logged to stderr but does NOT propagate the error to the MCP client.

`KB.AutoCommit` is `false` by default (zero-value) — so existing unit tests keep their behavior. The server enables it via `CARTOGRAPHER_GIT_AUTOCOMMIT` (default `true`). `sync_apply` is not wrapped: it writes into the client's `base_dir`, not into the KB.

**Rationale.** Commit-per-operation is the fundamental requirement for agent traceability: each change must be atomically identifiable in git history. The struct-level `false` default guarantees backward compatibility with tests and deployments that do not use git. The `true` default in the server aligns operational behavior with the documented expectation ("each write produces a git commit"). Step 2 (fetch/push, conflict handling) is out of scope for this iteration.
