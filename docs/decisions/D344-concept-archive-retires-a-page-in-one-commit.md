---
topic: control-plane
---

# D344 — `concept_archive` retires a page in one commit, and a lint check catches the hand-made retirement

**Decision.** A new `advanced`-tier tool, `concept_archive(id, if_match, [to], [status], [reason])`, sets the status (`deprecated` default, `archived` allowed), moves the page to `<to>/<last segment>` (default `archive`), rewrites inbound links and removes the entry from the source map's curated index, in one commit. A new info lint check, `index_lists_retired`, flags a curated index of a live map that still links a retired concept.

**Why.** Retiring by hand is four calls, and the first (patching the status while the page is still in its live map) makes `link_to_retired` fire on it. The reported symptom, a live index still listing a retired page, comes from the source-side index removal in `concept_move` running only for maps with `require_index_entry` and only for single-link lines; the later link rewrite then redirects the surviving entry to the archive path. `concept_move` is a generic rename and keeps that conservatism (D160: editing an index nobody declared curated is an assumption); retirement is explicit intent, so the new tool goes further. The destination side of `concept_move` was already done (D160) and is reused unchanged.

**Alternatives rejected.**
- A `drop_from_source_index` flag on `concept_move`: one intent should be one call, the flag keeps a "forgot the flag" failure mode, and `concept_move`'s description is at the D285 budget edge.
- Batch archiving: single concept keeps response and failure semantics simple; N pages are N calls. A batch can be a follow-up.
- `reason` in frontmatter: it would be a `nonstandard_field`, and archive is not a succession (`supersede_reason` has that role). It goes to `log.md` only.
- Writing the index finding into the scoped write-response lint (`scopedcheck.go`): an index finding is not tied to the written concept, and `concept_archive` already removes the entry.

**Consequences.** The source-index gate differs between the two tools on purpose (a comment next to `maintainCuratedIndexes` and a test for each side say so). The shared core is `applyConceptMoves`; changes to move semantics land there for both. A line citing several concepts is kept and reported, never edited. `index_lists_retired` can add info findings and `kb_status` conformance debt on existing KBs; a map accepts it with `lint_ignore` in `_map.md`. As a rule `link_to_retired` still fires once on the archived page for live pages that keep a link: that is the intended signal (D313).
