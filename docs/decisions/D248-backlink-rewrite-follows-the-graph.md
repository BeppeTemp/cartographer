---
topic: data-plane
---

# D248 — Backlink rewrite follows the graph

**Decision.** `concept_move` (and `concept_merge`) rewrites backlinks only in
the concepts that can hold one: those the cached link graph (D241) says link to
a moved id, those whose `superseded_by` names one (D243), and the moved
concepts themselves (D160). Each is read once, at its physical path, in walk
order. To make that exact, `RewriteLinks` resolves links exactly as
`ExtractLinks` does: code spans are masked and an extensionless href naming an
existing asset is not a concept (D150). `RewriteOutboundLinks` masks code spans
too. This amends D72 (the rewrite scope is the inbound graph, not the whole KB)
and aligns the rewrite with D150.

**Why.** A move read every concept, plus one `index.md` probe per concept: on
the largest real KB that is 12 MB per move, to change a handful of pages. The
graph already knows which pages link to the moved id. It could not be used
because extraction and rewrite disagreed: a "link" inside a fenced block or
inline code was rewritten by a move but never appeared in the graph. Aligning
them costs a deliberate behaviour change: an example link inside code is no
longer rewritten by a move, which is what D150 says it is — not a link.

**Alternatives rejected.**
- *Keep the full scan and only drop the per-concept probe*: halves the cost,
  keeps it proportional to the KB instead of to the backlinks.
- *Keep rewriting inside code and add code-span links to the graph as a
  separate set*: it would preserve an inconsistency D150 already ruled on, and
  keep a second link notion alive for one caller.
- *Rewrite via the adjacency without re-reading*: the graph caches links, not
  bodies (D241); the body has to be read to be rewritten.

**Consequences.** Extraction and rewrite must resolve identically: both go
through `mdLinkTarget`/`wikiLinkTarget` on the masked body, the trap is
commented at both functions, and `TestRewriteLinks_MatchesExtractLinks` pins it
as a property over generated bodies. `TestRewriteBacklinks_MatchesFullScan`
keeps a full-scan oracle; `TestWalkConceptsLinkingTo_ReadsOnlyCandidates` proves
a non-linking concept is not read, through an unexported `readRawHook` set only
from `export_test.go`. A candidate that vanished between the graph validation
and the read is skipped, as the walk skipped it.
