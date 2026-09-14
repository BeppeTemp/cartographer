---
topic: control-plane
---

# D32 — Search index persisted on SQLite (`internal/sqlindex`)

**Decision.** Introduced the `internal/sqlindex` package with the `modernc.org/sqlite` dependency (pure-Go driver, no cgo — resolves D3). Persists in `<root>/.cartographer/index.db` (gitignored via `.cartographer/`):
- `concepts(id, content_hash, body)` + FTS5 virtual table `concepts_fts` with `tokenize='trigram'` → **substring** keyword search (overcomes the "whole words only" limit of the in-memory inverted index, D12).
- `embeddings(id, content_hash, model, vec BLOB)` with the vector serialized as little-endian `float64`. *(This half is superseded by D135: the table is no longer created, read or written.)* `EmbeddingFresh(id, hash)` enables the **per-content-hash cache**: `index_rebuild` recomputes the Ollama embedding only for concepts whose hash changed (previously everything was re-embedded at every rebuild/startup).

Wiring: `RegisterKBToolsWithSQLIndex` (called by `main.go` when the embedder is active) registers `search`/`index_rebuild` variants that use `sqlIdx` if non-nil. `main.go` opens the per-KB DB **best-effort**: if `Open` fails (or FTS5 is unavailable) it logs to stderr and proceeds with the existing in-memory path. The previous in-memory functions and tools (`RegisterKBToolsWithEmbed`, `toolSearchWithEmbed`) remain for backward compatibility and for the `sqlIdx == nil` case. *(D36 update: unified registration in `RegisterKBTools(s, k, Deps)`; FTS5 is now active even without an embedder.)*

**Rationale.** The in-memory index is rebuilt at every startup by walking all concepts and — when the embedder is active — re-calling Ollama for each concept: expensive on large KBs and at every restart. SQLite persistence with a per-content-hash cache makes the embedding cost proportional to the *changed* concepts, and FTS5 trigram provides real substring search. `modernc.org/sqlite` is pure-Go (no cgo, no C toolchain) → consistent with binary portability. The best-effort fallback guarantees no environment regresses: without the DB, behavior is identical to before.
