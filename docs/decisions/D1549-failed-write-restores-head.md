---
topic: control-plane
---

# D1549 — A write that fails after touching the disk is rolled back by gitWrap

**Decision.** When a wrapped write returns an error (Go error or `IsError` result) and the working tree was verifiably clean before the handler ran, `gitWrap` restores the tree to HEAD before releasing the git lock: tracked changes reset, untracked files removed, ignored files kept. The error text says the changes were discarded. If the tree was not provably clean (the pre-commit of external changes failed, a conflict is open) or the handler succeeded and only its commit failed, nothing is discarded.

**Why.** `gitWrap` had no path for a handler that wrote and then failed (`concept_move` after applied moves, `concept_merge`, `kb_repair` contract rewrite, `concept_archive`). The residue stayed in the tree and the next write, even a `log_append`, committed it as `external changes` with the server identity, outside the write gate (D350, D907), with a possibly broken link and no history for the move. The cost: a handler error that deliberately wanted its partial state kept is no longer possible; none does.

**Alternatives rejected.**
- Commit the partial result under the tool's own subject with a trailer declaring it partial: the partial state never passed the write gate, and the commit would still be a half-done move.
- Make every handler roll back itself: a handler never locks or commits (`internal/mcpserver/AGENTS.md`), and the cases fixed at the cause (plan `kb-data-plane`) stay needed; this is the backstop.
- Discard also on a tree that was not clean: destroys an editor's unsaved work.

**Consequences.** The "clean before" test is `kb.TreeClean` after `commitExternalChanges`, so a git that does not answer discards nothing. A new write tool needs no code for this. D357's "external changes" now means edits made outside MCP only.

Known window: "clean before" is checked once, at the start of the call, so an outside edit written while the handler runs (the lock does not stop an editor) is discarded with the handler's residue. The window is the handler's duration; restricting the restore to the handler's own paths was rejected as it needs per-handler accounting (generated indexes touch paths no handler returns).
