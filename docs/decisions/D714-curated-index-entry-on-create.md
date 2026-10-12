---
topic: control-plane
---

# D714 — `index_entry` on creation adds a curated index entry, atomically in `concept_batch`

**Decision.** `concept_new` and a creating `write` operation of `concept_batch` accept `index_entry: true`, which appends `- [title](rel.md)` under a `## New pages` heading of the map's curated index, in the same commit as the concept. It applies to any non-generated index, whether or not the map declared `require_index_entry`; a generated index only yields a note.

**Why.** Bulk-creating drafts in a map with a hand-curated index cost an `index_get` and `index_patch` per map and a second commit, with a window where the KB was not conformant. The caller asking explicitly is the same intent as `concept_archive` (D344), so no map opt-in is needed.

**Alternatives rejected.**
- Always adding an entry on creation: editing an index nobody asked to touch is an assumption (D160).
- Allowing it on updates: where an existing page belongs in an index is the operator's prose.
- Guessing the thematic section: Cartographer cannot know it, so an obvious placement at the end (`## New pages`, like `## Moved here`) beats a wrong one in the middle.

**Consequences.** `WriteConceptBatch` rolls back only the files and log it wrote, so the `afterFiles` callback that edits indexes must restore the ones it patched before returning an error. `concept_new` has no such callback: the index is preflighted, and a later failure is a note, not an error. `concept_move` keeps `## Moved here`.
