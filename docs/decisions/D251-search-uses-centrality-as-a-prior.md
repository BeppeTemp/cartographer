---
topic: control-plane
---

# D251 — search uses centrality as a prior

**Decision.** `search` multiplies each hit's text score by `1 + β·p`, with
β = 0.2 and `p` the concept's PageRank percentile among the N concepts the
caller can see: the number with a strictly lower PageRank divided by N − 1, and
0 when N = 1. The prior is applied in the keyword-hit helper that `search` and
`graph_context` share (D242), on a candidate window of `min(3 × limit, 100)`
hits (never fewer than `limit`), after merge and before sort and cut. The
reported `score` is the boosted score.

**Why.** After D246 ranking is text relevance only, and in an operational KB
the runbook, the incident, the note and the service page often say the same
words: among them the order was effectively the id. The link structure says
which one the KB treats as the reference (D244). As a bounded multiplicative
prior it breaks near-ties without overriding a clearly better text match. The
cost is that `score` values change for every linked concept, and the backend
returns up to three times more candidates than the page.

**Alternatives rejected.**
- *Raw PageRank as the factor*: its scale depends on N, so the same β would do
  nothing on a large KB and too much on a small one; the percentile is
  scale-free and means the same on both backends.
- *Re-ranking only the returned page*: the prior could reorder the page but
  never let a central concept just below the cut into it.
- *A tie-break only on exactly equal scores*: BM25 scores are almost never
  exactly equal, so it would almost never fire.

**Consequences.** A concept at the minimum PageRank — every concept nothing
links to — gets `p = 0` and keeps its exact text score. For a narrowed
principal the percentiles come from `kb.LinkGraph(Visible)`, computed per call,
so a hidden hub never moves the order (D226); the whole-KB percentiles are
cached per link-graph view generation (D241). Ties after boosting still break
by id.
