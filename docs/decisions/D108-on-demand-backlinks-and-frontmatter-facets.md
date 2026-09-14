---
topic: control-plane
---

# D108 — On-demand backlinks and frontmatter facets

**Decision.** The link graph derives inbound and outbound adjacency on demand by
walking the KB with each concept's physical path. It is not persisted: vault
files remain the truth, including relative links inside expanded concepts, and
the modest walk avoids a second index reconciliation surface. `graph_neighbors`
selects `out`, `in`, or `both` direction on that same graph.

Structured predicates belong on `concept_list`, which inventories existing
concepts, rather than `search`, which ranks body text by relevance. Its small
frontmatter grammar intentionally supports only ANDed exact `key=value` and
`key!=value` predicates plus timestamp ranges. Regex and OR are absent: they
would make bounded inventory reads harder to predict without serving the
operational catalog questions this interface covers.
