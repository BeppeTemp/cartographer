---
topic: data-plane
---

# D2108 — A keyed type's own fields are never synonyms

**Decision.** `internal/kb/keyedtype.go` also lists the fields a keyed type owns: `IngestPlan.sources` and `IngestUnit.source` (type matched exactly, key in any case). `nonstandard_field` skips them, next to the exemption for a field the map contract names, so `concept_write`, `kb_repair` and the background repair leave them alone. It extends D853 and D1201.

**Why.** `sources` and `source` are synonyms of `provenance`, so the write path renamed an IngestPlan's `sources` to `provenance`, a later append of `sources` was refused as "has both", and the Surveyor's poll, which finds the plan through `sources`, recorded `no_ingest_plan`. D853 made the keyed types known to `unknown_type` only. The cost is a second small table kept beside the type predicates.

**Alternatives rejected.**
- Drop `sources`/`source` from the synonym table: other types (`Note`, `Source`) still want the rename to `provenance`.
- Exempt every field of every keyed type: `Source`, `Contradiction` and `Service` have no field the server needs under a synonym's name, so they keep their renames.
- Require the map contract to name the field: the workaround, but a plan in any other map is silently rewritten.

**Consequences.** A keyed type that gains a field the server reads under a synonym's name adds it to the table. The fields keep their names even when the page also has `provenance`: no "has both" finding.
