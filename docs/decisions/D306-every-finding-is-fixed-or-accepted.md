---
topic: data-plane
---

# D306 — Every finding is fixed or accepted, and a map accepts a style once

**Decision.** `lint_ignore` in a map's `_map.md` accepts the named checks for
every concept of the map and for the findings reported on the map. Besides every
check a concept may silence, it takes `facet_sprawl`, `missing_value_contract`
and `island`, which no concept owns. An `island` is also accepted by any of its
members. A map's top-level concept with no link in or out is now an `orphan`. A
`kb-doctor` session ends when the Observatory has nothing left to report.

**Why.** After a session took a real KB to zero warnings, the operator expected
the Observatory to be empty, and it still showed 256 `info` findings. Most were
choices, not defects. The KB writes "See also" items that say why each link
matters (137 `duplicate_link`), and services link the infrastructure they run on
(`map_misfit`). Accepting one choice cost one write per concept: 67 for the first.
Three checks could not be accepted at all, so an empty Observatory was not
reachable. The same KB showed nodes connected to nothing in the Atlas. `orphan`
exempted a map's top-level pages because the index reaches them, but the index
is not an edge, so a top-level page with no links was invisible to every check.
The cost of the change: a map-wide acceptance hides future instances of the
check in that map too. That is what accepting a style means, and the
`_map.md` commit records who decided it and why.

**Alternatives rejected.**
- *Hide `info` findings from the Observatory*: they are advice the KB had not
  answered yet. Hiding them makes "done" mean "not shown", not "decided".
- *A KB-wide acceptance*: maps differ in style (a journal and a reference map
  link differently), and a KB-wide switch is one edit away from turning a check off.
- *A new `isolated` check*: the defect is the gap in `orphan`'s exemption, and
  every tool that already reads `orphan` (gate, review, Observatory) reads it now.

**Consequences.** Map-level acceptance does not reach errors, nor directory-level
checks other than those named here. A KB upgrading may see new `orphan` warnings
for top-level pages with no links, which are real gaps in its graph.
`link_suggest` proposes the links.
