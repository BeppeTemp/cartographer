---
topic: client-configurator
---

# D180 — Every typed client-config field is written by `Save`

**Decision.** `Save` emits `search_depth`, `Load` strips it from `Extra`, and a
table-driven test asserts that every field of the YAML struct appears in the
written file.

**Rationale.**

- **The defect was invisible because a second bug hid it.** `SearchDepth` was
  read (`yamlConfig.SearchDepth`) but missing from the struct `Save` marshals,
  so it never reached the file from the typed field. It looked fine anyway,
  because `Load` did not strip `search_depth` from `Extra`, and `Save`'s merge
  wrote the stale raw value back. A file therefore round-tripped correctly while
  every programmatic change to the field was silently discarded — including the
  clamping D162 documents, which could never be persisted, so its warning
  repeated on every run.
- **Both halves had to be fixed together.** Fixing only `Save` leaves `Extra`
  shadowing the field, and the merge would keep the stale copy; fixing only
  `Load` starts dropping the value from disk entirely.
- **`Extra` is for genuinely unknown keys.** A known, typed field living there
  is a latent conflict, not a redundancy.
- **The guard is a test over the struct, not a comment.** This defect class is
  invisible on review — an omission from a literal — so the next one fails a
  test instead of shipping.

**Consequences.** None observable for a hand-written config: `omitempty` means a
zero `SearchDepth` still writes no key, so existing files are not churned.
