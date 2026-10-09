---
topic: control-plane
---

# D351 — A write answers with the fact lines it added that other concepts already carry

**Decision.** `concept_write`, `concept_new`, `concept_patch` and `concept_batch` add an advisory `repeated_fact` finding (`info`, no fix) to their `findings` for each fact line the write added that reaches the map's `repeated_fact_min` (default 3 owners including the written concept, never below 2). "Fact line" is `factLines`, the `kb_review` definition, called as is. "Added" means present in the new body and absent from the previous one. Owners are found through the search index and each candidate is re-read and kept only if its `factLines` hold the identical key. A call spends at most 5 lookups (shared across a batch) and a concept gets at most 5 findings.

**Why.** The drift is created by the write itself, but `kb_review` shows it only at the next doctor session. The check needs the previous body and an index, so it cannot live in `ScopedCheck` (pure, per concept) and `gate_check changed_ids` has neither. A full-text hit is only a candidate (terms are ANDed trigrams and ranked), hence the exact verification, which also makes the finding agree with the review. The previous body is read before the write, because afterwards every line would look added.

**Alternatives rejected.**
- A KB scan per write: cost grows with the KB; the index is already maintained and respects the caller's visibility.
- Putting it in `ScopedCheck`: no previous body, no index there.
- Reporting every repeated line, not only added ones: a rewrite of a page would re-report old debt on every touch.
- Consulting the other owners' maps for the threshold: the write answers for its own map's contract; the review remains the whole-KB authority.
- Cap of 20 lookups (the first plan figure): see the measurement.

**Consequences.** The finding is advice and never fails or delays a write beyond its budget. `keywordHitsReconciled` lets the finder reconcile the index once per call instead of once per lookup. Measured with `BenchmarkWriteRepeatedFact` (1,000 concepts, FTS5, `concept_batch` of 50 patches adding 3 fact lines each, Apple M5): one lookup costs about 6 ms and the single reconcile of the 50 written files about 50 ms, work the next search would otherwise pay. With a cap of 20 the batch took about +180 ms over the same batch without the finder; with 5, +45 to +75 ms, so the cap is 5 (`repeatedFactLookupCap`). Raise it only with a new measurement. The `kb_review` item stays the whole-KB authority; a change to `factLines` applies to both.
