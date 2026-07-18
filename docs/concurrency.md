# Concurrency, transactionality and git synchronization

## Single-writer model

The server is the **sole writer** of each KB's working copy, serialized by a **per-KB mutex** (`KB.mu`) that protects the entire disk-write + git-commit sequence. `main` is **protected**: the server never commits to it directly, but to a **per-KB working branch**.

Cycle: `stage → commit per logical operation → debounce/coalescing → (Server: open/update PR)`.

**Step 1 (local commit) — implemented.** Every write tool is wrapped by `gitWrap` (in `internal/mcpserver/gitwrap.go`), which acquires `KB.mu`, runs the tool, and on success calls `KB.CommitOp` (in `internal/kb/gitsync.go`). Enabled via `KB.AutoCommit=true` (default `false` at struct level, default `true` in the server via `CARTOGRAPHER_GIT_AUTOCOMMIT`).

**Step 2 (optional remote sync) — implemented.** If the KB has an `origin` remote and `KB.GitSync=true` (default `true` in the server via `CARTOGRAPHER_GIT_SYNC`), the wrapper runs `KB.SyncIn()` **before** the operation (fetch + `pull --rebase --autostash`) and `KB.SyncOut()` **after** the commit (push with a fetch+pull-rebase+push loop, exponential backoff cap 5). Without a remote both methods are no-ops: only the local commits from Step 1 remain. The primitive is `gitx.PullRebaseAutostash`; a rebase conflict is aborted and reported via `*gitx.RebaseConflictError` (which carries the conflicting files, LocalSHA, RemoteSHA, Branch). Rich handling of this error is Step 3. Multiple independent instances, each on its own clone of the same KB-remote, thus converge via git (Local Core profile); verified by the E2E scenario `08_git_multiclone`.

**Step 3 (agentic conflict handling) — implemented.** When `SyncIn` or `SyncOut` return a `*gitx.RebaseConflictError`, `gitWrap` (in `internal/mcpserver/gitwrap.go`) does the following for each conflicting file:
1. Converts the git path (`data/<path>.md`) to a ConceptID via `kb.GitPathToConceptID`.
2. Registers the conflict in `<root>/.cartographer/conflicts.json` (local, non-versioned state) via `KB.RegisterConflict`.
3. Marks the concept as `status: degraded` in the frontmatter (working tree, not committed) via `KB.MarkDegraded`.

The agent receives an `errorResult` with the count of registered concepts and is directed to the `conflicts_list` tool and the `kb-conflict-resolve` skill. The KB remains operational: writes to other (non-conflicting) concepts work; the `conflicts_list` tool is always available; `sync_check` reports the `open_conflicts` field. The bundled `kb-conflict-resolve` skill guides the agent step by step. Verified by the E2E scenario `09_git_conflict`.

**Step 4 (closing the resolution loop) — implemented (local profile).** The `git_conflict_resolve(concept_id, strategy, [body])` tool closes the loop without the agent having to touch git. `strategy ∈ {ours, theirs, edit}`: `ours` = local version, `theirs` = remote version, `edit` = reconciled content passed in `body` (full file, frontmatter included). The tool is **not** wrapped by `gitWrap` (it manages its own per-KB lock and must not re-trigger `SyncIn/SyncOut`, which would hit the same conflict again).

Two-phase mechanism (`internal/kb/conflicts.go`):
1. **Record** — `RecordResolution` saves the decision in the per-concept registry (`resolution_strategy`/`resolution_body`). As long as unresolved conflicts remain (`PendingConflictCount > 0`), git is not touched: the tool returns the list of pending items.
2. **Finalize** — when *all* open conflicts have a resolution, `FinalizeConflicts` runs **a single** transaction: stash uncommitted `degraded` markers → `git merge --no-commit --no-ff <remote_sha>` → overwrite each file with the resolved content (`ours`/`theirs` materialized via `git show <sha>:<path>` — avoids the `--ours/--theirs` swap of the rebase) and `git add` → rejects if conflicting files remain outside the registry → merge commit → `SyncOut` (best-effort push) → clears the registry and discards the `degraded` markers. On any git error: `merge --abort` + stash restore (working tree intact).

The record/finalize separation avoids a persistent "half-done" rebase/merge state between calls (crash-safe: the decisions live in the registry; on restart the finalize is re-run). Materializing the sides by content (`git show`) instead of with `--ours/--theirs` eliminates the footgun of the reversed semantics during rebase. The Server profile (working branch + PR) remains future work.

| Profile | Gate | Merge to main |
|---|---|---|
| **Local Core** | Lightweight gate (validate + lint + commit_gate) | Fast-forward on passing the gate; optional push to a personal remote. |
| **Server** | PR on a dedicated branch (readable diff, approval, audit) | On merge: mandatory rebase of the branch onto `main`, re-validate (gate), then fast-forward-merge and push. |

**Authority of `if_match`**: the content-hash is anchored to `main` at the moment the PR is opened. The pre-merge rebase is the point where two PRs on the same concept get reconciled: the second one fails with `stale_write` and must be replanned.

> **Operational constraint**: a single writer-server per KB repo. Write-replicating the same KB across different hosts is out of scope; scaling is done by partitioning different KBs across different instances.

## Git synchronization to the remote

- `fetch` before operating and periodically.
- On non-fast-forward push: `fetch + pull --rebase --autostash + push` loop with **exponential backoff, cap 5**.
- **Never** automatic force-push; intentional rewrite only with `--force-with-lease=<ref>:<sha> --force-if-includes`.
- `merge.conflictStyle=zdiff3`.
- **Atomic** per-file writes (write-temp + rename); the **git commit** is the multi-file transactional unit.

## KB state machine

| State | Meaning |
|---|---|
| `normal` | Operational. |
| `syncing` | Sync in progress. |
| `degraded (concept)` | One or more concepts are marked `status: degraded` due to an unresolved rebase conflict. The KB remains writable on other concepts; conflicts are visible via `conflicts_list`. |

**Resolving `degraded`**: the agent calls `conflicts_list`, reads the versions involved, decides on the reconciled content, rewrites the concept with `concept_write` (without `status: degraded`). The `sync_check` tool exposes the `open_conflicts` field for the SessionStart hook.

## Optimistic concurrency

- `concept_read` returns the **per-section content-hash** (normalized).
- `concept_write` with `if_match` fails with `stale_write` if the content has changed.
- **Advisory per-concept (or per-expanded-concept) lock** with TTL lease + heartbeat and `owner-id` persisted outside the versioned content (state file, not git). They expire on their own (no orphan locks on crash). They are released at the long-running checkpoint (Async Tasks); on resume each `if_match` is re-validated.
- Neither the hash nor the lock is tied to an MCP session (stateless-friendly).

## Crash recovery

On boot the server **detects and repairs** interrupted git state before serving the KB: half-done rebase → `abort`, leftover stash, orphan `index.lock`, crash between rename and commit.

See also: [`deployment.md`](deployment.md) §backup/DR for failures of external components.
