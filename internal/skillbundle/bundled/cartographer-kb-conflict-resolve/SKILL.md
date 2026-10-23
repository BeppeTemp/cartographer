---
name: cartographer-kb-conflict-resolve
description: Guide an agent through resolving open git rebase conflicts on degraded KB concepts.
version: "1.2"
---
# KB Conflict Resolve — Skill

## Purpose

When a `git pull --rebase` conflicts with a remote commit, Cartographer registers the conflict
and marks the affected concepts as `status: degraded`. This skill walks the agent through
resolving each conflict so the KB returns to a fully operational state.

## When to run this skill

Write operations fail with a message such as "git conflict detected and registered on N
concept(s)", or `conflicts_list` returns a non-empty list. (At operator level, `sync_check`
(not in `tools/list`) also reports `open_conflicts > 0`.)

## Procedure

### 1. List open conflicts

Call `conflicts_list` (no arguments). It returns a JSON array of conflict records, each with:

- `concept_id` — the ConceptID of the degraded concept (e.g. `shared/notes/c`)
- `local_sha` — the local HEAD SHA before the rebase was attempted
- `remote_sha` — the remote tip SHA after fetch
- `branch` — the branch name (e.g. `main`)
- `files` — all git paths involved in the same rebase conflict
- `detected_at` — ISO-8601 timestamp of detection

### 2. Read the current version

For each conflicting concept, call `concept_read` with its `concept_id`. The rebase was
aborted, and Cartographer marked the concept `status: degraded` in the working copy. The
`local_sha` and `remote_sha` fields of the record name the two sides (use `git show <sha>` at
operator level if you need to compare them directly).

### 3. Decide the reconciled content

Compare the content with your original intent:

- Merge any non-conflicting additions from both sides.
- Resolve factual discrepancies explicitly (do not silently discard either side).

### 4. Record a resolution for each concept

Call `git_conflict_resolve` once per record, with the record's `concept_id` and a `strategy`:

- `ours` keeps the local version, `theirs` the remote one — when one side wins whole;
- `edit` takes the reconciled **complete file** (frontmatter included, without
  `status: degraded`) in `body` — when you merged the two.

A call records the decision and answers how many conflicts are still pending. When the last
one is resolved, Cartographer merges the recorded remote commit, commits, clears the registry
and removes the `degraded` markers. A git failure on the way (merge, write, commit) aborts the
merge and keeps the registry, so the same calls can be retried. The push that follows is
best-effort: if it fails, the resolution stays committed locally and the next write pushes it.

### 5. Confirm

Call `conflicts_list` again: it answers "No open conflicts." once the transaction went
through. If a record is still listed (the resolution failed, or a new conflict arrived), repeat
from step 2.

> **Note**: `status: degraded` is a soft marker set by Cartographer to signal that the
> concept diverged from the remote. It is **not** a permanent status, and rewriting the
> concept with `concept_write` does not clear the conflict registry: only
> `git_conflict_resolve` does.

## Reserved files

`log.md`, `index.md`, `_map.md` and `_archive.md` are not concepts: Cartographer
reconciles their conflicts itself. If one appears in `conflicts_list` with
`kind: reserved` (its `concept_id` is the file path), resolve it with
`git_conflict_resolve`: `strategy: union` for a `log.md` (keeps both sides'
entries), otherwise `theirs` (remote) or `ours` (local). Never run git in the
KB clone.

## Reference

- Tool: `conflicts_list` — list open conflicts (read-only)
- Tool: `concept_read(id)` — read the current content and content-hash
- Tool: `git_conflict_resolve(concept_id, strategy, [body])` — record `ours`, `theirs`, `edit` (or `union`, reserved `log.md` only); the call that resolves the last conflict finalizes
