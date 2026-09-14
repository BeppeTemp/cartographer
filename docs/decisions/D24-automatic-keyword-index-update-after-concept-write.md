---
topic: control-plane
---

# D24 — Automatic keyword index update after `concept_write`

**Decision.** `toolConceptWrite` receives a `**search.Index` and, after each successful write,
calls `(*idx).Add(id, content)` to update the in-memory index incrementally. The
server no longer requires `index_rebuild` after each write. The post-write read error
is silent (the write is already confirmed).
**Rationale.** The agent should not have had to trigger `index_rebuild` manually: the correct
behavior is that search immediately reflects what was written. The incremental update
(single add) is O(n-terms) instead of the full rebuild's O(n-concepts).
Exception: with `RegisterKBToolsWithEmbed` active, `search` uses a separate index (`newIdx`)
that is not updated by `concept_write` — `index_rebuild` remains necessary in that path.
**Exception superseded by D36**: the index is now shared (`liveIndex`) across all paths and
`concept_write` also updates the persisted FTS5.
