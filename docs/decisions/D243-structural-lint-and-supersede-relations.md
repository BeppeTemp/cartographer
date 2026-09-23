---
topic: control-plane
---

# D243 — Structural lint and supersede relations

**Decision.** Lint gains five checks on the shape of the link graph and on the
`superseded_by` relation: `island`, `cut_concept`, `link_to_retired` and
`map_misfit` (`info`), and `broken_relation` (`warning`). They are computed once
per run on the whole KB and reported only for concepts in scope. `superseded_by`
stays a frontmatter relation that lint consults, not a graph edge. `supersede`
now refuses a missing successor and a concept superseding itself, and updates
both search indexes. `concept_move` rewrites a `superseded_by` that names a
moved concept, as it rewrites body links.

**Why.** Lint's only graph checks were `broken_link` and `orphan`, and the
commonest rot in an operational KB was invisible: live pages still citing
retired ones, groups of pages cut off from the rest, and successors that moved
while their predecessors kept pointing at the old id. `supersede` wrote without
touching the search indexes, so `search` showed the old status until a
reconcile.

The thresholds, each a named constant in `internal/lint`:
- `island` from 2 concepts: a lone one is already an `orphan`.
- `cut_concept` from 3 separated concepts: a page with a child or two is the
  normal shape of a wiki.
- `map_misfit` from 4 neighbours in maps with a two-thirds majority in one
  other map: below that, the right map is a matter of taste.

"Retired" is `deprecated` or `superseded`; `disputed` is not. `link_to_retired`
fires only from maps: an incident in a journal legitimately cites a component
that is gone today.

**Alternatives rejected.**
- `superseded_by` as a graph edge: it would change `orphan` and every degree
  for every existing KB.
- Community detection for `map_misfit`: assignments shift with unrelated edits,
  and the finding would flicker; a neighbour majority is direct and explainable.
- Computing on the scoped subgraph: a concept would get a different verdict
  depending on how lint was called.
- `island` as suppressible: it belongs to a component, not to any one
  concept's frontmatter.

**Consequences.** Existing KBs will show new `info` findings and possibly
`broken_relation` warnings; none is an error, so no `gate_check` that passes
today starts failing. The graph algorithms (`WeakComponents`,
`ArticulationPoints`) are in `internal/graphalgo`, checked against a
brute-force reference. A missing successor outside what the caller may write is
refused by the policy before the handler, with the same text as a hidden one.
