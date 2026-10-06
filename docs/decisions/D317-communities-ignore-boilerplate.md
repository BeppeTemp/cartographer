---
topic: architecture
---

# D317 — communities discount boilerplate links, anchor on internal ties, and link_suggest falls back to the title for an orphan

**Decision.** Three refinements of D242/D244. (1) Communities run Louvain on a
weighted graph: every edge is weighed by the normalised line that wrote it
(code masked, whitespace collapsed), and a line that N ≥ 5 of the visible
concepts carry weighs its edges 1/N; every other edge weighs 1. An undirected
edge takes the max of its two directed weights. (2) A community's anchor is the
member with the strongest ties inside it (the sum of its edge weights to other
members), then the highest total degree, then the smallest index. (3)
`link_suggest` on a concept with no link in either direction returns the
concepts the keyword search ranks for its title, with `score` 0, empty
`common` and `method: text_similarity`; every other response says
`method: resource_allocation`.

**Why.** A template that links every page to the same few hubs (monitoring,
inventory) gives each page identical edges. Unweighted, those edges dominate
the modularity: unrelated pages are welded into one giant community through the
hubs, and the hub, with the highest total degree, names it. Weighing by line
frequency removes that without a template registry: a line many pages share
is boilerplate whatever template produced it. The anchor uses *weighted*
internal ties, not the plain neighbour count the plan proposed: a hub every
member links to always has the most neighbours inside its community, so a
count would keep naming it after the reweighting; the weighted sum ranks its
discounted edges below a member's real ones. With no weights the sum is the
count, so an unweighted caller sees the plan's rule. For orphans, resource
allocation has no neighbour to start from and returned nothing, while the
`orphan` lint finding promised suggestions.

**Alternatives rejected.**
- *Sum the two directed weights of a mutual link*: double-counts a pair that
  links both ways, so mutual boilerplate would outweigh a single real link.
- *Count line frequency over the whole KB* (one cached `lineCount` on the
  graph view, as the plan sketched): a narrowed principal's partition would
  then depend on hidden concepts, a D226 leak. The cache keeps each entry's
  link lines (from the same read that extracts its links, no second walk) and
  the count is taken over the projected concepts on each `LinkGraph` /
  `GraphSnapshot`.
- *Discount by target in-degree instead of by line*: penalises a genuinely
  central concept that many authors chose to cite; the line is what tells a
  template from agreement.
- *Weight `ResourceAllocation` too*: no step asked for it and it already
  discounts hubs by 1/deg; left unchanged.
- *Search the whole title only*: search requires every term first and widens
  to any term only when nothing matches — the orphan's own title always
  matches, so a multi-word title found only itself. When the full title finds
  nobody else, each term is searched separately and the hits merged.

**Consequences.** Atlas colours and `atlas_overview` community names change on
template-heavy KBs. `link_suggest` responses gain `method` (additive). A link
whose text spans lines has no line and is never discounted. The threshold is
the constant `boilerplateThreshold` in `internal/kb/linkgraph.go`; the
uncached oracle in `graphcache_oracle_test.go` mirrors the weighting, so the
two must change together.
