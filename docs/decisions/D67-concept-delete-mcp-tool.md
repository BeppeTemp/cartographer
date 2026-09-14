---
topic: control-plane
---

# D67 — `concept_delete` MCP tool

**Context.** The control-plane exposed `concept_write` (create/update), `concept_move` (move),
`supersede` (mark superseded) but no way to actually **remove** a concept: deleting a
page required direct git on the repo (clone + `git rm` + push, then the pod realigns on pull),
outside the MCP control-plane.

**Decision.** New `concept_delete(id, [if_match])` with the standard write invariants:
`KB.DeleteConcept` rejects empty IDs and reserved files, resolves the path in write-mode (blocks
traversal) and `os.Remove` with `ErrNotFound` if absent; optional `if_match` for optimistic
concurrency (`stale_write`). Registered under `gitWrap` → automatic git commit (`CommitOp`
also stages removals). Updates the derived indexes: `search.Index.Remove` (exported wrapper
of the existing `remove`) via `liveIndex.remove`, and `sqlindex.Index.Delete` (best-effort, log to
stderr, like `concept_write`'s upsert). Like `concept_move`, it does **not** update incoming
backlinks: the return message points to `lint`. Classified write (no need to touch
`readonly.go`, fail-closed default); added to the `agent` tool profile (D65) as a core tool.
