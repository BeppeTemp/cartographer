---
topic: control-plane
---

# D89 — Multi-term search fallback and coherent search modes

**Status: implemented (2026-07-24).**

**Decision.** FTS5 keyword queries tokenize on whitespace and first require all usable terms; an empty result retries once with any term. Tokens shorter than three characters are excluded from the trigram MATCH, preserving the prior whole-query behavior when no usable token remains. The in-memory index follows the same all-terms-then-any-term policy. Both search profiles expose `mode: keyword|semantic|hybrid`; `use_semantic=true` remains a deprecated alias for `mode=hybrid`.

**Rationale.** Requiring a whole multi-word trigram phrase was stricter than the in-memory all-terms behavior and missed documents containing the terms apart. The OR retry improves recall only when the more precise result is empty, while preserving ranking and ordinary keyword behavior. This unifies the public schema without changing the capability gate: as established by D43 and AD11, semantic/hybrid search still requires a server started with Ollama and keyword search remains available without it.
