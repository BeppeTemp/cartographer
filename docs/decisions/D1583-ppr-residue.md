---
topic: control-plane
---

# D1583 — Personalized PageRank credits the unpushed residue

**Decision.** `graphalgo.PersonalizedPageRank` returns `p + alpha*r`: the mass left
below the push threshold (`eps*deg`) counts as the walk's own, so the neighbours of a
hub seed score above 0. `graph_context` and the related block of `search` rank on it.

**Why.** A seed hub of degree 2000 gives each neighbour a residue of about 4e-4, below
`eps*deg` for degree 4 and up, so forward push never moved it into `p` and
`rankNeighbours` dropped every such neighbour: `graph_context` on an index page
returned only the leaves. The cost is that scores move a little (the residue is added
to every node) and the pass is O(N) in the graph size, not O(1/(alpha*eps)) alone.

**Alternatives rejected.** Normalising the push threshold by the seed's degree: it
keeps the exact push semantics but pushes far more nodes for a hub seed, so it loses
the bounded cost. Keeping more precision in `rankNeighbours`' cut: changes the
rounding of every score in the output.

**Consequences.** Known limit: `rankNeighbours` drops a node whose score rounds to 0
at four decimals (`round4`). With alpha 0.25 a neighbour of a seed hub of degree 4000
or more scores about 4.7e-5 and still rounds to 0, so it is dropped; the fix lands
with a change of that rounding, which is a response-contract change of its own.
