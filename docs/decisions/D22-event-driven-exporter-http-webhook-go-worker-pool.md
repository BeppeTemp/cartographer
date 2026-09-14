---
topic: data-plane
---

# D22 — Event-driven exporter: HTTP webhook + Go worker pool ✅ Removed

**Removed (June 2026).** The `internal/exporter` package and the `source_ingest` tool were deleted.
Ingest is now the agent's direct responsibility via `concept_write` with no intermediaries.
See D28 for the removal rationale.
