---
topic: control-plane
---

# D1768 — A delete removes the concept's line from its map's curated index

> **Amends [D714](D714-curated-index-entry-on-create.md)**: the server that adds the index line on creation removes it on deletion.

**Decision.** `concept_delete` and the `delete` operation of `concept_batch` remove, in the same commit as the delete, the entry of the deleted concept from the curated `index.md` of its map, with the removal `concept_move` and `concept_archive` already use (`removeCuratedEntry`): the line whose only link is the id, with the lines that continue it, matched as `[[id]]` or as a relative `(path.md)` link. The result reports it in `curated_indexes` (`concept_batch`) or as a line of the message (`concept_delete`). A generated index is left to the server. As a backstop, `ScopedCheck` of a concept that is gone reports `broken_link` on the map's curated index when a line still links it.

**Why.** The curated index is not in the concept graph, so neither `deleteBlockers` nor the write gate saw its link: every delete in such a map left a dead line, found later by a whole-map lint. Creation already edits the index when asked (D714), and a delete has no choice to ask for: a line to a page that is gone is never right.

**Alternatives rejected.**
- Leaving it to the caller (`index_patch` after the delete): a second commit and a window where the KB is not conformant, the cost D714 removed on creation.
- A second line parser: `removeCuratedEntry` already encodes the cases (continuation lines, a line citing another concept) that were fixed once.
- Dropping a line that cites the id beside other concepts: that is prose the operator wrote. It is kept, and the backstop refuses the delete with `broken_link`, so the caller edits the line and deletes in one `concept_batch`.

**Consequences.** The edit is made under the caller's grant on the map as `index_patch` asks it (a grant with no type): a token narrowed by type deletes its page, the index stays, a note says the line is still listed, and the backstop does not refuse it because the caller cannot see the index. A note never quotes an index line, which may name pages outside the caller's view. Lines are read with fenced blocks masked once over the whole index, as the lint reads them. Only a two-segment id has a map index. An expanded concept's own `index.md` is a concept of the graph: a satellite still listed there is an inbound link that refuses the delete, and the index of a deleted expanded concept goes with it. A failed index edit is a note, not an error: the delete stands and the gate names the line.
