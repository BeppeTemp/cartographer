---
topic: control-plane
---

# D1327 — `concept_batch` gains `delete`; a list in a result is `[]`, never `null`

**Decision.** `concept_batch` takes a third operation, `delete` (`id`, optional `if_match`). A link from a page deleted in the same batch does not block a delete, and the write gate judges nothing on a page that is gone, so a closed group of pages (two that link each other) is deleted in one call; a link from any page outside the batch still refuses the whole batch with `inbound_links`. `concept_delete` is unchanged. Separately, every list in a tool result or Atlas response is `[]` when empty, never `null`: `search` (so the Atlas search too), `concept_move`/`concept_archive`/`concept_merge` `rewritten`, `gate_check` and `validation_errors` lists and `graph_neighbors`' `neighbors` are built non-nil (#1327, #1328).

**Why.** Deleting A and B that link each other was impossible: `concept_delete` refuses on the inbound link of the other, `force` does not lift it in a git KB, a batch had no delete, and removing the links first leaves each page an orphan. The group has to go in one atomic step, and the batch is the existing atomic unit. A `null` where a list is documented makes every client special-case the empty answer.

**Alternatives rejected.** Letting `concept_delete` take a list of ids: a second batch primitive with its own rollback. Making `force` ignore inbound links: it would leave broken links in a git KB, which the gate refuses. Deferring the inbound check to the write gate: a KB without git has no gate, so the batch checks the links itself. Normalizing every nil slice centrally in `jsonResult`: it cannot tell an `omitempty` field, absent on purpose, from a bare list, so the builders that marshalled nil were fixed one by one.

**Consequences.** A delete inside a batch of an expanded concept is refused (it may own assets: `concept_delete` with `force`). A linker the batch writes or patches is judged on its content after the batch (body links and, for a Source, provenance), so relinking and deleting are one call, as `concept_delete`'s advice says. `kb.BatchWriteOp.Delete` is rolled back with the rest, from the same snapshot as a write. A new tool result that marshals a slice builds it non-nil (`emptyIfNil`, `protocol.go`).
