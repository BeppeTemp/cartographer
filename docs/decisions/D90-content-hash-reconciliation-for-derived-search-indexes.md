---
topic: control-plane
---

# D90 — Content-hash reconciliation for derived search indexes

**Status: implemented (2026-07-24).**

**Context.** Incremental index updates previously occurred only through MCP writes. Imports, manual filesystem edits, and concepts pulled by git therefore left a non-empty SQLite index stale; startup only rebuilt when the table was empty, so one service restart did not repair drift.

*(Superseded in part by D136: the `index_rebuild` tool it coexisted with is now `reindex(full: true)`.)*

**Decision.** SQLite stores one content hash per concept, so reconciliation walks the KB, compares its hashes with `AllHashes()`, and incrementally upserts new/changed concepts and deletes vanished ones. The in-memory index follows the same add/remove path as MCP writes. Reconciliation runs at boot, after a successful `SyncIn` that changes HEAD, and through the write-scoped `reindex` MCP tool. `cartographer reindex [--kb <name>]` calls a healthy configured server over HTTP; only while it is down does the local administrative CLI open `<kb>/.cartographer/index.db` directly.

**Rationale.** Hash reconciliation makes the index converge without an always-on watcher, preserves the server's single owner of a live SQLite connection, and avoids needless embedding work: changed hashes naturally miss the existing embedding cache until a semantic rebuild/search refreshes them. A filesystem watcher (`fsnotify`) was rejected for this release: it adds platform-specific lifecycle and event-loss complexity while still requiring reconciliation after imports, git pulls, and restarts.
