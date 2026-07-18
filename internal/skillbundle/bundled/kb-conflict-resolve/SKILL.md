---
name: kb-conflict-resolve
description: Guide an agent through resolving open git rebase conflicts on degraded KB concepts.
version: "1.1"
---
# KB Conflict Resolve — Skill

## Purpose

When a `git pull --rebase` conflicts with a remote commit, Cartographer registers the conflict
and marks the affected concepts as `status: degraded`. This skill walks the agent through
resolving each conflict so the KB returns to a fully operational state.

## When to run this skill

Write operations fail with a message such as "git conflict detected and registered on N
concept(s)", or `conflicts_list` returns a non-empty list. (At operator level, `sync_check`
also reports `open_conflicts > 0` — it is not advertised to agents under the default
"agent" tools profile.)

## Procedure

### 1. List open conflicts

Call `conflicts_list` (no arguments). It returns a JSON array of conflict records, each with:

- `concept_id` — the ConceptID of the degraded concept (e.g. `shared/notes/c`)
- `local_sha` — the local HEAD SHA before the rebase was attempted
- `remote_sha` — the remote tip SHA after fetch
- `branch` — the branch name (e.g. `main`)
- `files` — all git paths involved in the same rebase conflict
- `detected_at` — ISO-8601 timestamp of detection

### 2. Read the current (local) version

For each conflicting concept, call `concept_read` with its `concept_id`.

The content reflects the local state **after the rebase was aborted** — Cartographer resets
to the remote state and adds `status: degraded` to the frontmatter. The `local_sha` field
in the conflict record tells you what the pre-conflict local version was (use `git show
<local_sha>` at operator level if you need to compare the two versions directly).

### 3. Decide the reconciled content

Compare the current (local/remote) content with your original intent. Produce the
authoritative reconciled version:

- Merge any non-conflicting additions from both sides.
- Resolve factual discrepancies explicitly (do not silently discard either side).
- Remove `status: degraded` from the frontmatter.

### 4. Write the reconciled concept

Call `concept_write` with:
- `id`: the `concept_id` from the conflict record
- `frontmatter`: the reconciled frontmatter **without** `status: degraded`
- `body`: the reconciled markdown body

The write will trigger a fresh `SyncIn` (fetch + pull-rebase). Because the local history
has been reset to the remote state, the rebase succeeds and the write is pushed cleanly.

### 5. Clear the conflict record

After a successful `concept_write`, call `conflicts_list` again to confirm the concept is
no longer listed. If it still appears (e.g. because the write also conflicted), repeat
from step 2.

> **Note**: `status: degraded` is a soft marker set by Cartographer to signal that the
> concept diverged from the remote. It is **not** a permanent status — removing it via
> `concept_write` is the resolution action.

## Reference

- Tool: `conflicts_list` — list open conflicts (read-only)
- Tool: `concept_read(id)` — read the current content and content-hash
- Tool: `concept_write(id, frontmatter, body, [if_match])` — write the reconciled version
