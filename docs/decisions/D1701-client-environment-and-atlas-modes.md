---
topic: sync-provisioning
---

# D1701 — The client environment section and the Atlas client modes come from the resolver, not from a second reading of the table

**Decision.** The "subagent sentence" of the instructions block (D154) becomes `environmentSection`: the original
sentence unchanged for native agents, then a line for agents installed as skills, one for what the resolver skipped
(`ApplyOptions.Skipped`, filled by the caller from the same notes the sync summary prints, D1700) and one for installed
hooks whose event the client never fires. A client that is invoke-only gets its `InvokeHint`. The Atlas gets
`provisioning.ClientModes`, which runs `ResolveProjections` over the KB's own artifacts per provider (global scope) and
returns `native`, `degraded`, `skipped` (with the reason) or `unverified` per client; the panel shows a tag for all but
`native`. `unverifiedFor` is the one per-artifact rule `Apply`'s `unverified:` line and the panel share.

**Why.** Both are statements about what a client received; computing them anywhere but the resolver would let the panel,
the block and the summary disagree. A requirement on an artifact the KB does not ship is `unverified`, not `skipped`:
the Atlas sees one KB, the client may bind several.

**Deviations accepted.** The skipped line is not grouped per KB (the notes carry no KB; adding one would change every
`ProjectionNote` literal). The Atlas judges the global scope only; a project-scope cell that differs is not shown.
A skipped artifact is named only when it is authorized (`AuthorizedSkipped`, looked up by `kind/name` in the source manifest).

**Rewrite trigger and the server path.** The environment section moves with things no artifact hash records, and it cannot
join the recorded paths-section hash: `status` recomputes that hash without the approvals and resolver notes a sync holds
and would report drift for good. Its trigger is the block itself: `applyInstructionsGroup` builds the whole body before
the trigger check and compares it with the block on disk (`blockBodyIs`), so the section is never located by line prefix
(a KB's instructions can end with any line). The server `sync_apply` now runs `ResolveProjections` and applies the
resolved manifest, so it writes the same block a client would; it holds no approvals, so a gated artifact is never named.
Every KB-supplied name or reason in the lines is quoted and bounded; the native "Subagents installed by the KB" line is
not quoted (those agents are authorized and their KB already writes the instructions text, and quoting would change
Claude Code's bytes).

**Number.** Provisional: the PR number is not known yet; rename to `D<pr>-...` when it is opened (D672).
