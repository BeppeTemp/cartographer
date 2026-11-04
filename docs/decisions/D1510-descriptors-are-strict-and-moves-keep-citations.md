---
topic: control-plane
---

# D1510 — An unreadable map descriptor refuses writes, and a move keeps the citations of a Source

**Decision.** Two contract changes of the KB data plane review (#1510).

- A map descriptor that exists and cannot be read refuses every write its
  contract would judge (`concept_write`, `concept_batch`, `concept_move`,
  `concept_transfer`), with a message that names the descriptor. A descriptor
  that is absent (`fs.ErrNotExist` or `okf.ErrNotFound`) still means "no
  constraint". `map_create` builds the descriptor with `Frontmatter.Set` and
  `Serialize`, and refuses a title with a line break and a concept type that is
  empty or holds `,`, `[` or `]`.
- `concept_move` rewrites a `provenance` entry equal to the old ID of a moved
  concept, as it does for `superseded_by` (D243), so a moved Source stays cited
  (D278). `concept_transfer` cannot: a citation cannot name a page of another
  KB, so it leaves the entry and lists the pages in `provenance_not_rewritten`.

**Why.** Every error of `ReadArchiveMeta` meant "no constraint", so a
`_map.md` with broken frontmatter turned the strict contract off for four write
paths without a word. And the documented behaviour of a move (a `provenance`
entry is only a `mentions` entry of kind `frontmatter`) left the Source
`source_uncited`, although the server uses that relation for `deleteBlockers`
(D278) and the orphan check (D720). Confirmed by the maintainer on 2026-11-04.

**Alternatives rejected.** Keep the documented behaviour of the move and record
it as intended: a citation is a relation the server already reads, so leaving it
stale is a defect, not a choice. Refuse only on a parse error and not on an
unreadable file: a permission error would still switch the contract off.
Concatenate the descriptor and escape by hand: `UpdateMapContract` already uses
the serializer, and one writer cannot drift from its reader.

**Consequences.** A map whose `_map.md` is broken accepts no write until it is
fixed; `kb_repair` and `lint` still work, because they do not go through the
contract check. A `provenance` entry that names a moved concept follows it, so
the `mentions` of a move no longer report it. The graph facets carry the
`provenance` list, which is what lets a move find the citing pages without
reading the KB.
