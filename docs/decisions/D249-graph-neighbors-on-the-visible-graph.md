---
topic: control-plane
---

# D249 — graph_neighbors on the visible graph

**Decision.** `graph_neighbors` walks the subgraph induced by the concepts the
caller can see, for every principal: a hidden concept is neither a result nor a
transit node, and a link to one is dropped rather than reported `missing`. The
policy refusal of `depth > 1` for tokens that cannot read the whole KB is gone.
A missing start id still answers backlink inspection, but a narrowed token keeps
getting `not found` for any id that does not exist, as for every read tool.

**Why.** The walk ran on the whole file graph and the handler only filtered the
output, so a narrowed traversal could reach a visible concept through a hidden
one and reveal the path. Refusing depth > 1 was the only safe fix while that
held. D242 computes its tools on the visible-induced graph; applying the same
rule here makes the refusal unnecessary. The cost is one visibility check per
visited node, on a cached graph (D241).

**Alternatives rejected.**
- *BFS over `kb.LinkGraph(Visible)`* (the plan's wording): `LinkGraph` holds
  existing concepts only, so broken targets — reported today at their distance —
  would need a second pass, and admin output would no longer come from the same
  code path. A visibility predicate on `Links.Neighbors` (`NeighborsWithin`)
  keeps admin output byte-identical by construction.
- *Letting a narrowed token start from a missing id* (also in the plan): the
  policy gate answers `not found` for a missing id before any handler runs, so
  that a hidden concept (refused by type, for instance) and a missing one look
  alike. Exempting `graph_neighbors` would turn it into an existence oracle.
- *Keeping the depth limit*: safe but pointless once the walk cannot cross a
  hidden node.

**Consequences.** Any new traversal tool filters nodes during the walk, never
only its output. `kb.GraphNeighbors` stays principal-free for lint. A narrowed
token's answer equals an admin's on the KB with the hidden concepts deleted,
minus the links to them (pinned by `TestGraphToolsNarrowedEquivalence`).
