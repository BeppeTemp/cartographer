---
topic: data-plane
---

# D347 — A journal's open state can come from a contract field, not only from `status`

**Decision.** The map contract gains `open_field: <key>`. For a concept of that map the open/closed state is the value of that frontmatter key, read against `open_statuses`; a concept without the field (absent, empty or not a string) falls back to `status`. Closed is `status: archived`, or a non-empty field value that is not open. In such a map `status` is a lifecycle field, so `status_semantics` and `status_reclassify` do not fire. `harvest_after: 0` now means off; a negative value removes the key.

**Why.** A KB that keeps the outcome of an entry in a domain field (`open`, `mitigated`, `resolved`) and `status` for `active`/`archived` was invisible to `work_list`, `stale_open` and `harvest_candidate`. One key reusing `open_statuses` adds no second list to keep in sync. Treating every non-open value as closed means a KB's own vocabulary is never silently ignored for not being in a done/resolved synonym family.

**Alternatives rejected.**
- Deriving the open set from the `field_values` vocabulary: a vocabulary lists values, not which of them mean open.
- A second list `closed_statuses`: two lists to keep consistent, and unknown values would fall in neither.
- A new finding or review kind: the existing checks read the effective state, which keeps a map without `open_field` bit-for-bit unchanged.

**Consequences.** `openPhase` and `closedPhase` take the frontmatter, not a status string, so a new reader of open/closed must go through them (`effectiveState` in `internal/lint/decay.go`). `harvest_after: 0` in `_map.md` stops being `contract_malformed`, and `map_update harvest_after: 0` no longer removes the key (use a negative value). `WorkEntry.State` carries the field value and `by_status` groups by it.
