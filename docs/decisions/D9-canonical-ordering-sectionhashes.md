---
topic: data-plane
---

# D9 — Canonical ordering + SectionHashes

`CanonicalString()` sorts YAML keys alphabetically (without comments). `SectionHashes` computes per-section hashes (h1/h2) + `_full`. Two files with keys in different order produce the same hash.
