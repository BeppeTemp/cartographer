---
topic: control-plane
---

# D3 — SQLite: `modernc.org/sqlite` planned, not yet introduced

For the FTS5 trigram index, `modernc.org/sqlite` is preferred (pure Go, no cgo). To be introduced when the in-memory index is no longer enough (large KBs, semantic search in SQLite). *(Superseded by D32/D43: introduced as `internal/sqlindex` — FTS5 trigram + embedding cache, best-effort with in-memory fallback.)*
