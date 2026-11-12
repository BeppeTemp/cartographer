---
topic: control-plane
---

# D1986 — The cluster projection has its own preview route

**Decision.** The Atlas API serves the cluster projection of D1983 as its own
preview route, `GET /kbs/{kb}/graph/clusters`, with `scope`, `unfold` (at most 50
cluster ids, more is `400`) and `focus` (one concept id), next to the flat
`/graph`. It passes the caller's visibility predicate, so the ranking and the
community member lists come from the memo kept per graph generation and visible
set (D1980): two principals with different rules never share an entry. Scope,
unfold and focus are derived per request.

The Atlas reads it when the KB's overview counts more visible concepts than
the route's `threshold`, unless the URL says `graph=nodes`; the unfolded cluster
ids live in the URL (`unfold=`), are emptied on a KB switch and lose what the
server reports in `dropped_unfolds`, and the selected concept rides every
request as `focus`.

**Why.** The body is a different shape from the flat snapshot (clusters, weighted
edges, mixed elements), and the flat route's contract stays as it is. A preview
route is a free-form object in OpenAPI, so the shape can still move before it is
promoted. The cost is a second graph route a client has to choose between.

**Alternatives rejected.**
- Extending `/graph` with a mode: the flat route's contract would move with it.
- Memoizing the aggregated cluster edges too: their weights depend on the scope
  and the unfold set, and at 50,000 concepts and 250,000 links the aggregation
  costs a small fraction of building the visible graph the request needs anyway.
- Waiting for the overview before any graph request: on a large KB the
  overview is the slow request, so the page would stay empty; a KB whose size
  is not known yet is read flat once, and clustered from then on.
- An unbounded `unfold` list: far more than 50 ids never fit the 400-element
  budget, so a longer list is a malformed client, not a view.

**Consequences.** The route never takes a predicate other than the caller's
`Visible`: the memo's key is the visible set, and a broader predicate would hand
a narrowed principal sizes and weights over concepts it cannot see (D226, D244).
An unknown, invisible or out-of-scope `focus` answers exactly like no focus, so
the Atlas asks for a concept it does not draw once, never in a loop.
