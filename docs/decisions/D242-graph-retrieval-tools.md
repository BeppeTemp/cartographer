---
topic: control-plane
---

# D242 — Graph retrieval tools: graph_context, link_suggest, graph_path

**Decision.** Three read-only tools run graph algorithms on the link graph.
`graph_context` seeds from a question's top `search` hits and from given
concepts, ranks the rest by personalized PageRank (restart 0.25, forward-push
threshold 1e-4) over the undirected projection, and explains each result with
its hop count and the concept it was reached through. `link_suggest` proposes
links by resource allocation with at least two shared neighbours.
`graph_path` returns the shortest chain between two concepts and is advanced.
All three run on the subgraph induced by the concepts the caller may see. The
algorithms live in a new pure package, `internal/graphalgo`, over the int-indexed
projection `kb.LinkGraph(include)`.

**Why.** An agent needing the context around a topic chained `search`,
`concept_read` and `graph_neighbors` one hop and one call at a time, with
unranked sets, and `graph_neighbors` beyond one hop is refused to narrowed
tokens. PPR (as in HippoRAG) ranks multi-hop context continuously and
discounts hubs smoothly, where a BFS cut at "distance ≤ k" needs hub blocking
(as Graphify does) and ranks nothing within a ring. A restart of 0.25 lets 2–3
hop context score on graphs with a mean undirected degree of 6–9; HippoRAG's
0.5 is tuned for much denser phrase graphs. Resource allocation penalises
hub-mediated evidence more than Adamic–Adar, and a single shared neighbour is
too weak to act on. Removing hidden concepts before computing — rather than
filtering results — is what makes the tools safe for a narrowed token without
a whole-KB restriction: scores, distances and paths computed on the whole
graph would carry what the caller cannot see. The cost is one projection per
call: 3–5 ms on the synthetic 1,000-concept KB of D241 (`graph_context` warm
3.2 ms, `link_suggest` 5.3 ms, `graph_path` 5.3 ms, most of it the cache
validation).

**Alternatives rejected.**
- gonum: a dependency for a few short algorithms at ≤ 5k nodes, with less
  control over determinism than ascending-order loops.
- One tool with a `mode` switch: `search` dropped its modes for coherence
  (D135); each tool has one job.
- Filtering results by visibility after computing on the whole KB: leaks
  hidden concepts through the numbers.
- Raw search scores as seed weights: FTS5 and the in-memory index score on
  different scales; the reciprocal rank does not depend on the backend.
- The plan's PPR test bound (L1 ≤ 1e-3 at eps 1e-5): forward push only
  guarantees eps·Σdeg, a few 1e-3 on the test graphs. The test checks that
  bound at 1e-5 and ≤ 1e-3 at 1e-6.

**Consequences.** Every output is deterministic: loops run in ascending node
order, ties break by id. A missing and a hidden concept give the same
`not found` text. `kb.LinkGraph` took the name the D241 accessor had; that one
is now `kb.Links`. Later graph work builds on `internal/graphalgo` and
`kb.LinkGraph`.
