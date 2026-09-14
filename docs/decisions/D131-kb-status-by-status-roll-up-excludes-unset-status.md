---
topic: control-plane
---

# D131 — `kb_status`: `by_status` roll-up excludes unset status

**Decision.** `kb_status` gains a `by_status` map (status value → count),
built in the same `WalkConcepts` pass that already produces `by_type`.
Concepts whose frontmatter does not declare `status` are left out of the
map entirely, not bucketed under an empty-string key — mirroring how
`concept_list`'s `where` predicates already treat a missing key (D108).
`by_status` is initialized as an empty map like `by_type`, so a KB with no
`status` values in use serializes it as `{}` rather than omitting the key.

**Rationale.** `status` is already a free-form optional OKF field that
`concept_list` filters on exactly (D108); without an aggregate, an agent
can only discover which values are in use by guessing and calling
`concept_list` once per guess. Excluding unset status avoids a giant `""`
bucket dominating the map in the common case where only a minority of
concepts track status — the aggregate should answer "how much of this KB
is in each declared state", not restate `total`. No new predicate grammar,
task/workflow semantics, or status-value validation is introduced: values
stay owned by each KB, exactly as scoped by issue #119.
