---
topic: architecture
---

# D1983 — Past 2,000 visible concepts the link graph is projected onto its communities

**Decision.** `KB.GraphClusters` projects the caller's visible link graph onto
the Louvain communities of D244. Above `ClusterThreshold` (2,000, the node limit
where the flat snapshot starts truncating) in-scope visible concepts, each of the
150 largest communities is one cluster and the rest share one tail cluster
`c:~tail`; at or below it the answer is the flat snapshot. A cluster id is `c:`
plus the id of the member with the highest PageRank of the whole community
(ties: smallest id), its label that member's title, and a cluster-cluster edge
is one undirected edge whose weight counts the underlying directed links. A
cluster unfolds into its members by PageRank; one response holds at most 400
clusters and nodes and 1,500 edges. The verbs are unfold and fold, never expand
(`GraphNode.Expanded` already means a folder concept).

**Why.** A 10k-50k concept KB was drawn as its top 2,000 nodes, the rest simply
absent. Clusters and counts are computed from the visible graph the snapshot
builds, after visibility and before scope, so a principal never sees a size, a
label or a weight that includes a concept it cannot see (D226, D244). The member
lists live in the ranking memo keyed by generation and visible set, so no
request regroups the graph. The Louvain rank is not a usable id: a new concept
reorders sizes. The top-PageRank member is stable until a revision changes the
top of that community, and then the id changes and an unfold of it is reported
as dropped.

**Alternatives rejected.**
- Cluster id from the Louvain rank: changes with every new concept.
- Label from the community anchor (D244): chosen to name a legend, it can differ
  from the most important member.
- A hierarchical Louvain: one level is enough for a response of a few hundred
  elements, and unfolding gives the second.

**Consequences.** Anything that groups the visible graph reuses
`buildVisibleGraph`, never the unfiltered cache. An unfold that does not fit the
element budget (at least 50 members) is reported as dropped rather than drawn
over budget.
