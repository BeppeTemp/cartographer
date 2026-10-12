---
topic: control-plane
---

# D716 — map_oversize is reported on the write that crosses the threshold

**Decision.** `concept_write` (creation), `concept_new` and `concept_batch` carry one `map_oversize` finding when the concepts they created take a map from at most its threshold to above it. In a batch it is computed once, from the whole created set, and lands on the result entry of the last created op of that map. Updates, satellites, journals, archived pages and maps with `lint_ignore: [map_oversize]` never trigger it. The check, count and wording are shared with the whole-KB pass (`mapOversizeFinding`).

**Why.** `map_oversize` is a whole-KB check, so a bulk import only learned about it at the next `lint`. `ScopedCheck` cannot host it: it has no "before" state and runs once per ID even inside a batch, so it cannot see a batch taking a map from 40 to 70. The creating handlers know which IDs are new, so `before = current - created` and the crossing test are cheap (one cached graph read). It stays advisory `info` and outside `writeLintFindings`, so it never reaches `write_gate` or `gate_check changed_ids`.

**Alternatives rejected.**
- A `dry_run` for `concept_batch`: a new contract for a batch tool, not needed to fix this.
- Reporting on every write into an oversize map: contradicts D312 (report only what the write introduced).
- Putting it in `ScopedCheck`: cannot see the created set or the before count.
- A new field in the batch response: the per-op `findings` shape stays unchanged.

**Consequences.** `ScopedCheck` still excludes `map_oversize`; its doc comment points to `MapOversizeCrossings`. `concept_move` into another map is not covered (the next `lint` reports it). Amends D312 and D313.
