---
topic: control-plane
---

# D12 — Keyword search: pure-Go inverted index

`internal/search`: lowercase tokenization on non-alphanumeric boundaries, multi-term AND, TF scoring, scope filtering (concept ID prefix). No substring matching (queries must be whole words). Trigram can be added in the same package without changing the interfaces.
