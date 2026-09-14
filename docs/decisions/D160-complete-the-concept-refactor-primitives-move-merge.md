---
topic: data-plane
---

# D160 — Complete the concept refactor primitives: move, merge, collapse

**Status: implemented (2026-08-28).** Amends [D77](D77-atlas-map-journal-hierarchy-the-dossier-becomes-a.md) (which recorded no inverse for
`concept_expand`). Requires [D149](D149-one-link-base-and-one-concept-resolver-for-lint.md). Closes #181.

**Context.** Reorganising a KB is routine, and the three refactors that come up most each left the
author with a manual link-repair pass.

1. **`concept_move` rewrote inbound links only.** Documented and working — but the moved concept's own
   relative links broke whenever the directory depth changed, and curated indexes were untouched: the
   source map's index kept listing the concept (`broken_link`) and the destination's did not mention it
   (`index_incomplete`).
2. **Merging a satellite into its parent broke links three ways**: the merged body's own links were
   relative to the satellite's directory, links **to** the deleted satellite remained in both syntaxes,
   and so did links from **sibling** satellites. Done seven times in one session, each merge needed a
   manual repair pass.
3. **There was no inverse of `concept_expand`**, while `expanded_as_category` actively advises going
   back above eight children. The only route was a batch `concept_move` by hand.

**Decision.**

- **The outbound rewrite has no flag.** A move that leaves the moved body's own links broken is simply
  incomplete; a flag would preserve that as a supported mode. `RewriteOutboundLinks` consults the
  batch's `moveMap` first, so two concepts moved together that link each other compose correctly —
  the case a naive implementation gets wrong, and one the tests pin.
- **`relLink` is exported rather than copied.** `cmd/cartographer/import.go` had a private copy whose
  comment said it existed "to avoid exporting KB-internal move machinery for a single caller". There
  are now two callers, so the copy is gone: a third would have been the bug.
- **Curated indexes are maintained only where a contract asked for it** (`require_index_entry`).
  Editing an index nobody declared as curated would be an assumption, not a fix.
- **Both index edits are conservative.** A source line is removed only when the moved concept is its
  only link; a line citing two concepts is kept and **reported**, because it is prose the operator
  wrote. The destination entry is appended under a `## Moved here` heading created if absent:
  Cartographer cannot know the right thematic section, and a wrong placement in a curated document is
  worse than an obvious one at the end.
- **`concept_merge` merges a satellite into its own parent only.** Arbitrary concept-into-concept
  merging is a bigger design — whose frontmatter wins, which map — and is out of scope, which the
  tool's description says. Inbound redirection goes through the same whole-KB `rewriteBacklinks` pass
  a move uses, so **sibling satellites need no special case**: that is the third breakage, handled by
  construction.
- **`concept_collapse` does not change the ID**, because expansion never does: `<id>/index.md` becomes
  `<id>.md` under the same ID and no inbound link needs rewriting. It refuses while satellites or
  **assets** remain — assets live in the directory and only an expanded concept can hold them, so
  moving them silently would be worse than refusing — and names them.
- **Both new tools are `advanced`**: large-refactor tooling, same tier as `concept_batch` (D125).
  Registered and callable by name, not advertised in `tools/list`, so the default working set stays
  small. `TestServer_ToolsProfile` is the golden test that forces the classification.

**Consequences.** `concept_move` now edits more than it used to, so an operator with a manual
post-move routine should stop doing it. The strongest assertion available for all three is that a
follow-up `lint` reports neither `broken_link` nor `index_incomplete`, and that expand-then-collapse
returns the original content hash — a round trip, rather than a claim that the inverse is one.
`docs/data-plane.md` no longer says there is no inverse.
