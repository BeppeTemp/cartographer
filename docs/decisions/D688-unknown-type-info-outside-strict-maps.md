---
topic: data-plane
---

# D688 — unknown_type is info outside the maps that adopted types

**Decision.** An `unknown_type` finding with no case-insensitive match in the
palette is `info` when the page's map is neither `ontology_mode: strict` nor
strict about templates (`StrictTemplates`), and `warning` otherwise. Its message
names the fix: declare the template `kb_review kind=template_proposal` proposes,
or set the type to a declared one. A case variant stays a `warning` with its
`set_value` fix.

**Why.** Since every check runs by default (D371), a KB that declares templates
for a few types lit one warning per page of every other type on upgrade (60+ on
a real KB), all solved by one template. A per-page warning for a missing
KB-level declaration fills the doctor's queue with noise; where a map has
adopted types, the same finding is real drift and keeps its weight.

**Alternatives rejected.** One finding per (map, type): the case-variant fix is
per page, and a grouped finding has no path for `lint_accept` or the Atlas to
point at. Silence until the map adopts templates: the hint toward the template
proposal is still useful, and info keeps it visible without counting.

**Consequences.** The severity of `unknown_type` depends on the page's map
contract, not only on the registry; a change to `StrictTemplates` or to the
strict ontology mode changes it too.
