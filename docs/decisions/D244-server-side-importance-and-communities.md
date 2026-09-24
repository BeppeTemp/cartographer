---
topic: architecture
---

# D244 — server-side importance and communities

**Decision.** The server computes a global PageRank (directed, damping 0.85,
dangling mass spread uniformly) and a community partition (Louvain in node
order, no randomness, then every community split into its connected
components) on the graph of the concepts the caller can see, before scope and
limit. The graph snapshot carries `pagerank` and `community` per node and the
ordered `communities` list; past its limits it keeps the most important nodes
and edges instead of the first ids. The Atlas colours by the server's
communities and no longer runs Louvain. `atlas_overview(structure: true)`
lists the most central concepts and the main communities for agents. This
amends D226's truncation rule and settles D234's note that any reduction for a
large KB happens on the server.

**Why.** Truncation by id dropped an alphabetical tail of the KB however
central it was, and communities computed in the browser on the returned
snapshot recoloured a scoped or truncated view and were invisible to agents.
Computing both once, on the visible graph, fixes the three at once, and
computing them on the *visible* graph keeps D226's rule that the numbers
disclose no hidden concept. The cost is a PageRank and a Louvain run per
snapshot request, which at the 5,000-node ceiling is milliseconds.

**Alternatives rejected.**
- *Full Leiden*: its guarantee that no community is disconnected is what
  matters, and splitting Louvain's communities into components gives it for a
  fraction of the code; Leiden's speed advantage is irrelevant at ≤ 5k nodes.
- *Keep Louvain in the browser*: the recolouring of scoped views and the
  agent-blind structure would stay.
- *Structure on every `atlas_overview`*: the tool opens most sessions, and ~25
  extra lines per call is context nobody asked for.

**Consequences.** The ranking, anchor and slot rules of the old
`communities.ts` (size, then smallest member; anchor by degree, then id; twelve
hues, the rest slot 0) now live in `graphalgo.Communities` and must stay there.
The UI does not use `pagerank` yet: node size and list order are still by
degree. Dropping `graphology` and `graphology-communities-louvain` shrank the
gzipped bundle from 306.8 KiB to 287.0 KiB.
