---
topic: control-plane
---

# D36 — mcpserver: unified `Deps` registration, split per domain, shared index

**Decision.** Three structural interventions on `internal/mcpserver` (July 2026):
1. **Split per domain**: `tools.go` (2548 lines) is split into `tools_read.go`, `tools_search.go`, `tools_write.go`, `tools_governance.go`, `tools_skill.go`, `tools_sync.go`; only registration and shared helpers remain in `tools.go`.
2. **Unified registration**: the 5 `RegisterKBTools*` variants are replaced by a single `RegisterKBTools(s, k, deps Deps)` with `Deps{Embedder, VecStore, SQLIndex, BundleFS}` (nil fields = capability absent). `search`/`index_rebuild` are also a single implementation driven by `deps` (the response `mode` fields remain `keyword`/`hybrid`/`keyword_fts5`/`hybrid_fts5`).
3. **Shared keyword index**: the double pointer `**search.Index` is replaced by the concurrency-safe `liveIndex` wrapper (RWMutex — over HTTP, tools may run concurrently), **shared** between `concept_write` and `search` in all paths. `concept_write` also updates the persisted FTS5 index (`SQLIndex.Upsert` best-effort). This **supersedes the exception documented in D24**: writes are immediately searchable in the embed/SQLite paths too.

Additionally, `main.go` now passes `SQLIndex` **always** (no longer only with Ollama active): FTS5 substring keyword search is available even without an embedder, as intended by D32. `use_semantic=true` with no embedder configured returns an explicit application error instead of a latent panic.

**Rationale.** The 5 registration functions grew combinatorially with each optional capability; the `Deps` struct makes adding a capability O(1). The triplication of `search`/`index_rebuild` duplicated logic that silently diverged (the D24 exception was the symptom). The `tools.go` monolith made navigation expensive; the split follows the same categories as `control-plane.md` §MCP API.
