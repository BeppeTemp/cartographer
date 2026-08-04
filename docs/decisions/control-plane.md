# Control-plane, search and lint decisions

MCP tools, indexing, search, lint and read/write ergonomics. Current behavior: [`../control-plane.md`](../control-plane.md).

These records explain why choices were made and may describe superseded behavior.
For the supported interface, follow the current-state page linked above.

<a id="d3"></a>
## D3 — SQLite: `modernc.org/sqlite` planned, not yet introduced
For the FTS5 trigram index, `modernc.org/sqlite` is preferred (pure Go, no cgo). To be introduced when the in-memory index is no longer enough (large KBs, semantic search in SQLite). *(Superseded by D32/D43: introduced as `internal/sqlindex` — FTS5 trigram + embedding cache, best-effort with in-memory fallback.)*

---

<a id="d10"></a>
## D10 — `concept_write`: frontmatter from JSON map
The tool receives the frontmatter as a JSON `map[string]interface{}`; the key order in the file depends on Go map iteration (random). It does not affect the `ContentHash` (canonical ordering). File readability can be improved by building the `Frontmatter` with a conventional order.

---

<a id="d12"></a>
## D12 — Keyword search: pure-Go inverted index
`internal/search`: lowercase tokenization on non-alphanumeric boundaries, multi-term AND, TF scoring, scope filtering (concept ID prefix). No substring matching (queries must be whole words). Trigram can be added in the same package without changing the interfaces.

---

<a id="d14"></a>
## D14 — Lint: deterministic checks only in the Core
Only `broken_link`, `stale_claim`, `orphan`. Reasoning checks (cross-model deep lint) deferred to the Server profile. `Now` for stale_claim is injectable for testability.

---

<a id="d20"></a>
## D20 — Embedding: interface + Ollama adapter, wired
`internal/embed`: `Embedder` interface with `Embed(text) → Vector`. `OllamaEmbedder` adapter (HTTP POST `/api/embed`). In-memory `Store` with cosine similarity. Activated with `--ollama <url>` (or `CARTOGRAPHER_OLLAMA`): the `search` tool supports `use_semantic=true` for hybrid mode (keyword + vector); `index_rebuild` also rebuilds the vector store.

---

<a id="d24"></a>
## D24 — Automatic keyword index update after `concept_write`
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

---

<a id="d32"></a>
## D32 — Search index persisted on SQLite (`internal/sqlindex`)

**Decision.** Introduced the `internal/sqlindex` package with the `modernc.org/sqlite` dependency (pure-Go driver, no cgo — resolves D3). Persists in `<root>/.cartographer/index.db` (gitignored via `.cartographer/`):
- `concepts(id, content_hash, body)` + FTS5 virtual table `concepts_fts` with `tokenize='trigram'` → **substring** keyword search (overcomes the "whole words only" limit of the in-memory inverted index, D12).
- `embeddings(id, content_hash, model, vec BLOB)` with the vector serialized as little-endian `float64`. `EmbeddingFresh(id, hash)` enables the **per-content-hash cache**: `index_rebuild` recomputes the Ollama embedding only for concepts whose hash changed (previously everything was re-embedded at every rebuild/startup).

Wiring: `RegisterKBToolsWithSQLIndex` (called by `main.go` when the embedder is active) registers `search`/`index_rebuild` variants that use `sqlIdx` if non-nil. `main.go` opens the per-KB DB **best-effort**: if `Open` fails (or FTS5 is unavailable) it logs to stderr and proceeds with the existing in-memory path. The previous in-memory functions and tools (`RegisterKBToolsWithEmbed`, `toolSearchWithEmbed`) remain for backward compatibility and for the `sqlIdx == nil` case. *(D36 update: unified registration in `RegisterKBTools(s, k, Deps)`; FTS5 is now active even without an embedder.)*

**Rationale.** The in-memory index is rebuilt at every startup by walking all concepts and — when the embedder is active — re-calling Ollama for each concept: expensive on large KBs and at every restart. SQLite persistence with a per-content-hash cache makes the embedding cost proportional to the *changed* concepts, and FTS5 trigram provides real substring search. `modernc.org/sqlite` is pure-Go (no cgo, no C toolchain) → consistent with binary portability. The best-effort fallback guarantees no environment regresses: without the DB, behavior is identical to before.

---

<a id="d36"></a>
## D36 — mcpserver: unified `Deps` registration, split per domain, shared index

**Decision.** Three structural interventions on `internal/mcpserver` (July 2026):
1. **Split per domain**: `tools.go` (2548 lines) is split into `tools_read.go`, `tools_search.go`, `tools_write.go`, `tools_governance.go`, `tools_skill.go`, `tools_sync.go`; only registration and shared helpers remain in `tools.go`.
2. **Unified registration**: the 5 `RegisterKBTools*` variants are replaced by a single `RegisterKBTools(s, k, deps Deps)` with `Deps{Embedder, VecStore, SQLIndex, BundleFS}` (nil fields = capability absent). `search`/`index_rebuild` are also a single implementation driven by `deps` (the response `mode` fields remain `keyword`/`hybrid`/`keyword_fts5`/`hybrid_fts5`).
3. **Shared keyword index**: the double pointer `**search.Index` is replaced by the concurrency-safe `liveIndex` wrapper (RWMutex — over HTTP, tools may run concurrently), **shared** between `concept_write` and `search` in all paths. `concept_write` also updates the persisted FTS5 index (`SQLIndex.Upsert` best-effort). This **supersedes the exception documented in D24**: writes are immediately searchable in the embed/SQLite paths too.

Additionally, `main.go` now passes `SQLIndex` **always** (no longer only with Ollama active): FTS5 substring keyword search is available even without an embedder, as intended by D32. `use_semantic=true` with no embedder configured returns an explicit application error instead of a latent panic.

**Rationale.** The 5 registration functions grew combinatorially with each optional capability; the `Deps` struct makes adding a capability O(1). The triplication of `search`/`index_rebuild` duplicated logic that silently diverged (the D24 exception was the symptom). The `tools.go` monolith made navigation expensive; the split follows the same categories as `control-plane.md` §MCP API.

---

<a id="d43"></a>
## D43 — Automatic SQLite index rebuild at startup if empty, without embedding

**Decision.** `<kb>/.cartographer/index.db` is gitignored and derived: after a fresh clone (e.g.
k8s pod restart) it starts empty and `search` answered `count=0` until someone called
`index_rebuild` manually. At startup, for each mounted KB, if `ix.Count() == 0` the server rebuilds
FTS5 from the `.md` files (`mcpserver.EnsureSQLIndexFresh`, reuses `rebuildSQLIndex`), deliberately
**without embedding**. Best-effort: an error logs and does not prevent startup.
**Rationale.** No embedding at boot because Ollama may be slow/absent — blocking
startup on an optional external dependency would be the wrong coupling; the cache
repopulates anyway at the first `index_rebuild`/semantic search. `COUNT(*)==0` is the cheapest
signal and consistent with the "derived and rebuildable index" invariant.
Details: `docs/control-plane.md` §Search index.

---

<a id="d65"></a>
## D65 — "agent" tool profile and compact instructions: less fixed context per session

**Context.** Every agent session paid ~4.8k tokens of fixed cost: 32 tools in `tools/list`
(~3.4k tokens of schemas) and an instructions block in CLAUDE.md of ~1.4k tokens, of which ~500 were
the **duplicated** agent descriptions (already injected by the client's agent registry, where
the agent is installed natively) and the page counts were mutable state inside an imprinting
artifact. Of the 32 tools, only ~half serves the agent in a normal session: the rest is
operator governance/maintenance or plumbing called by name from the client CLI
(`sync_pull` from `connect`/`sync`/`status`, `sync_check` from the SessionStart hook), which does not go
through `tools/list`.

**Decision.**
1. **Tool profile** (`tools.profile` YAML / `CARTOGRAPHER_TOOLS_PROFILE` / `--tools-profile`,
   default **`agent`**): `tools/list` exposes only the core set (17 tools: read/search/write,
   content structure, plus `conflicts_list`+`git_conflict_resolve` for the auto-recovery of the
   `kb-conflict-resolve` skill); the 15 advanced tools (`advancedToolNames`,
   `internal/mcpserver/visibility.go`, marked **[A]** in `control-plane.md`) are hidden but
   remain **callable via `tools/call`** — visibility is not authorization, which stays with
   scopes/RBAC. `profile: full` restores the complete list. The `Server` zero-value = full
   (no surprise for library users); the `agent` default lives in `config.Default()`.
   Golden test `TestServer_ToolsProfile`: every new tool must be classified or the test fails.
2. **Compact instructions** (`generateKBInstructions`): archives as inline names only (no
   page counts — the hash no longer changes with every page added), operational instructions from 6 to
   3 lines, agents as **names only** (the descriptions stay in the `kind: agent` artifact,
   translated and installed natively per provider). Auto-generated block: from ~680 to <100 tokens.

Measured result: fixed cost per session from ~4.8k to ~1.9k tokens (17 tools ≈ 1.86k of schemas
+ reduced block), with the daily flow unchanged (search → read → write → log).

**Amended by D123.** `validate`, `lint`, `gate_check`, and `kb_status` moved
from the advanced set into the `agent` profile's core set: they are
read-only governance the documented agent loop depends on, and a
descriptor-bound MCP host cannot call a tool `tools/list` never advertised.
The counts above are the state as decided here, not current; see D123 for
the measured cost of the change and `control-plane.md` for the current
core/advanced split.

**Discarded alternatives.** Consolidating governance into a single `kb_admin(action=...)` (fewer
tools but a more opaque umbrella schema, and it does not solve the plumbing); filtering `tools/list` by token
scope (entangles visibility and authorization: an rw agent token would still see everything);
also hiding `conflicts_list`/`git_conflict_resolve` (it would break auto-recovery: a client
cannot call unlisted tools); exposing the advanced tools only when conflicts exist via
`notifications/tools/list_changed` (elegant, but non-Claude providers do not re-fetch
reliably).

---

<a id="d66"></a>
## D66 — `kb_overview` counts concepts, not subdirectories

**Context.** `toolKBOverview` printed for each archive `DossierCount`, which via `ListDossiers`
counts **subdirectories**. A flat-concept KB (e.g. `homelab-wiki`: `entities/x.md`, not
`entities/x/`) has 0 subdirectories → `0 dossiers` for each archive, and an agent reads it as
"empty KB" despite having dozens of healthy concepts (observed with Codex on 2026-07-05: 62 concepts,
`search`/`concept_read` ok, overview at `0 dossiers`).

**Decision.** New `KB.ConceptCount(archive)` recursively counts non-reserved `.md` files
(`WalkDir` + `okf.IsReserved`, excludes `index.md`/`log.md`/`_archive.md`/`AGENTS.md`).
`kb_overview` shows `(N concepts)` per archive, and `(N concepts, M dossiers)` when
subdirectories also exist — so flat KBs and dossier-based ones both stay readable. Requires
server redeploy.

---

<a id="d67"></a>
## D67 — `concept_delete` MCP tool

**Context.** The control-plane exposed `concept_write` (create/update), `concept_move` (move),
`supersede` (mark superseded) but no way to actually **remove** a concept: deleting a
page required direct git on the repo (clone + `git rm` + push, then the pod realigns on pull),
outside the MCP control-plane.

**Decision.** New `concept_delete(id, [if_match])` with the standard write invariants:
`KB.DeleteConcept` rejects empty IDs and reserved files, resolves the path in write-mode (blocks
traversal) and `os.Remove` with `ErrNotFound` if absent; optional `if_match` for optimistic
concurrency (`stale_write`). Registered under `gitWrap` → automatic git commit (`CommitOp`
also stages removals). Updates the derived indexes: `search.Index.Remove` (exported wrapper
of the existing `remove`) via `liveIndex.remove`, and `sqlindex.Index.Delete` (best-effort, log to
stderr, like `concept_write`'s upsert). Like `concept_move`, it does **not** update incoming
backlinks: the return message points to `lint`. Classified write (no need to touch
`readonly.go`, fail-closed default); added to the `agent` tool profile (D65) as a core tool.

---

<a id="d70"></a>
## D70 — Incremental update ergonomics: `concept_patch` + snippets in `search` results

**Status: active.** Implemented as planned (WP1–WP3 below): pure string-replace on the body
only (no section-aware), title/snippet in all search modes (in-memory, FTS5, hybrid).

**Context.** Analysis of a real session (2026-07-07, Sonnet 5, wiki closure after an
HA "bedroom fan" change) showed two control-plane frictions:
1. to add a few lines to a concept the agent had to re-read and rewrite the entire
   body (~9k chars) via `concept_write` — and before the write it *instinctively* attempted an
   old_string/new_string `Edit` call on a nonexistent local copy of the concept: models
   expect a patch-style affordance that is missing today. The full rewrite costs tokens and risks
   silent loss of sections through hallucination;
2. `search` returns only `{id, score}` (`searchHit` in `tools_search.go`): every hit forces
   a full `concept_read` to figure out whether it is relevant.

**WP1 — `concept_patch` tool.** New tool in `tools_write.go`, semantics identical to the clients'
`Edit` tool: `{id, old_string, new_string, replace_all?, if_match}` with `if_match`
**mandatory** (a patch only makes sense on an existing, already-read concept). Handler:
`ReadConcept` → check `if_match` (`stale_write` error like `concept_write`) → replacement
on the **body only** (dedicated errors: `old_string_not_found`, `old_string_ambiguous` on
multiple matches without `replace_all`) → reuse of `concept_write`'s write path (frontmatter unchanged
except an optional `frontmatter` parameter with shallow-merge for bumping `aggiornato`/`fonti`;
liveindex, `sqlIdx.Upsert`, git commit via gitwrap): extract a shared helper instead of
duplicating. Register in `RegisterKBTools` and classify **rw** in `readonly.go`. Note: the
`okf.SectionHashes` already exist — a future section-aware patch is possible, but the first
iteration stays pure string-replace (simpler and matching the expected affordance).

**WP2 — `search` with `title` + `snippet`.** Extend `searchHit` with `Title` and `Snippet`
(~200 chars) in all modes (in-memory keyword, FTS5, hybrid). FTS5: native auxiliary
`snippet()` function in `SearchFTS` (`internal/sqlindex`). In-memory/semantic: excerpt around
the first occurrence of the term (fallback: first lines of the body); title from the frontmatter —
if the liveindex does not hold it already, add it there, **no** per-hit `ReadConcept` inside the
search handler. Response budget: with `limit` 20 and 200-char snippets the payload stays <5k chars.

**WP3 — Documentation and closure.** `docs/control-plane.md` §MCP API (new tool + new
search fields), this entry (from PLANNED to active), and the release tracking
state then in use. Tests in
`server_test.go`: patch happy-path, `stale_write`, `old_string_not_found`/ambiguous,
`replace_all`; snippets in `internal/sqlindex` and in the in-memory fallback.

**Companion action (outside this repo, to do at activation).** Update the curated
`instructions.md` of the `homelab-wiki` KB (via `concept_write`): a "KB-first" rule
mirroring the closure rule — before exploring HA/cluster for an already known entity,
`search` the KB and put the concept's digest into the explorer's mandate. In the analyzed session the
KB already contained the device's details but was consulted only in writing, after the work was done.

**Order.** WP1 and WP2 independent (parallelizable); WP3 closes. No migration: both
are additive and backward compatible (clients using only `concept_write` do not change).

---

<a id="d71"></a>
## D71 — MCP tools for provisioning artifacts: `artifact_read`/`artifact_write`/`artifact_list`/`artifact_delete`

**Status: implemented (2026-07-09).** Delta versus the plan below: (a) the `if_match` is the
"pure" sha256 of the single file's raw content, not the aggregate hash of
`provisioning.ContentHashDir` (which concatenates multiple files and includes paths — unsuitable for a
per-file if_match); (b) for the `instructions` kind, `artifact_list` reads the raw file
`instructions.md` instead of the content *generated* by `BuildManifest` (otherwise the if_match
would never match `artifact_read`/`artifact_write`, which operate on the raw file);
(c) path guard: `safeJoin` extracted from `kb.ResolvePath` + new `kb.ResolveRootPath`
anchored to `kb.Root`; (d) mcp descriptor validation reuses D69 via the new exported wrapper
`provisioning.ParseMCPServerSpec`. YAML flag: `kbs[].allow_artifact_write`
(→ `deployment.md`). WP3 confirmed with no work: the revision changes with the content on disk.

**Context.** Real session (2026-07-07): to tighten the description of
`skills/opencode-dev/SKILL.md` in the `homelab-wiki` KB, the repo had to be cloned from Gitea,
edited and pushed by hand — the control-plane covers only `data/` (concepts), while provisioning
artifacts (`skills/`, `agents/`, `hooks/`, `mcp/`, `instructions.md`) can only be edited via
git (`sync.md` §channels). Two frictions: (1) an MCP-only agent cannot self-maintain its own
skills/agents; (2) a direct git push is not seen by the server until the first MCP write
(`SyncIn` fires only in `gitWrap`) — in the session it had to be forced with a `log_append`.

**Decision.** Four tools in `internal/mcpserver/tools_artifact.go` operating on the artifact
files at the KB root with the same invariants as concepts (per-KB lock, commit
per operation, `SyncIn`/`SyncOut` via `gitWrap`). No new data plane: they are the files already
scanned by `provisioning.BuildManifest`.

**WP1 — Path guard and tools.**
- `path` relative to `kb.Root`, canonicalized (`filepath.Clean`, no `..` nor symlink escape
  — reuse/extract the data plane's safe-join helper), allowed only if it matches:
  `skills/<slug>/**`, `agents/<slug>.md`, `hooks/**`, `mcp/<slug>.json`, `instructions.md`
  (lowercase-hyphenated slug). File size cap (256 KiB) and path length cap.
- `artifact_list()` → `[{kind, name, files: [{path, sha256}]}]`: reuses `BuildManifest`'s
  scan (without bundle) so kind classification stays in one place.
- `artifact_read(path)` → content + `sha256`.
- `artifact_write(path, content, if_match?)`: on an existing file `if_match` **mandatory**
  (= sha256 of the current content, the same hash as `ArtifactFile`; `stale_write` error);
  on a new file `if_match` absent (`already_exists` error if the file is there). Per-kind
  validation **before** the write: `skills/*/SKILL.md` → frontmatter parse + `skill.Validate`
  (name == folder, description present; rejection on blocking issues); `agents/*.md` →
  frontmatter with `name`+`description` (same requirements as the `kbAgents` scan);
  `mcp/*.json` → D69 descriptor parse; `hooks/**` and `instructions.md` → cap only.
- `artifact_delete(path, if_match)`: removes the file (and the directory left empty).

**WP2 — Registration, scopes and profiles.**
- `RegisterKBTools`: `artifact_read`/`artifact_list` always; `artifact_write`/`artifact_delete`
  only if `KBSpec.AllowArtifactWrite` (new per-KB YAML flag, **default false**): writing a
  skill means injecting instructions the clients will execute — the capability must be granted per-KB
  by the operator, an `rw` token alone must not imply it. `homelab-wiki` will enable it.
- `readonly.go`: `artifact_list`/`artifact_read` read-only; write/delete absent → rw
  (fail-closed already correct).
- `visibility.go`: `artifact_read`/`artifact_write` agent-visible (the point is
  self-maintenance); `artifact_list`/`artifact_delete` advanced (they remain callable via
  `tools/call` by name). Update the `TestToolsProfile` and `TestReadOnlyToolsGolden` goldens.
- Wrapping: `gitWrap` on write/delete + `notifyWrap("notifications/skills/list_changed")`
  when the path is under `skills/` (same signal as `skill_install`).

**WP3 — Sync and revision: no work expected, to be verified in tests.** The manifest
revision is recomputed by `BuildManifest` at every `sync_check` and changes with the content on disk;
the existing layered triggers (`sync.md`) propagate to the clients. The MCP write goes through
`SyncIn`, so it also absorbs concurrent git pushes: friction (2) disappears via the main path,
and the direct git push remains supported as today.

**WP4 — Documentation and closure.** `control-plane.md` §MCP API (4 tools), `sync.md` (MCP
edit channel alongside git), `skills-services-secrets.md`, `deployment.md` (`KBSpec` flag),
this entry (from PLANNED to active), and the release tracking state then in
use. Tests in `server_test.go`: path whitelist
(traversal, disallowed kind), `stale_write`/`already_exists`, invalid SKILL.md rejected,
profile goldens, flag disabled → write/delete not registered.

**Companion action (at activation).** Curated `instructions.md` of the `homelab-wiki` KB:
document the `artifact_write` flow instead of "artifacts only via git".

**Order.** WP1 → WP2 sequential; WP3 is verification; WP4 closes. All additive and
backward compatible (no migration; current clients do not change).

---

<a id="d78"></a>
## D78 — Readable per-path log, `concept_read` with size guard and outline, `concept_oversize` lint

**Status: implemented (2026-07-14).**

**Context.** Real-use analysis: (1) `log_append(path=...)` prefixes `[<path>] ` and always writes to the root log (an architectural TODO never resolved), but `log_tail(path=...)` read `<path>/log.md` — nonexistent — silently returning empty: the entries existed but were invisible to the agent. (2) A real 92 KB concept blew through the MCP client's token budget on `concept_read`, forcing a workaround outside the MCP surface (direct file read). No guard prevented either the full read of a huge body or a concept's growth beyond a reasonable threshold.

**Decision.**

a) **Root-log-with-prefix confirmed** (the per-directory log remains deferred, YAGNI): `log_tail(path)` now reads any entries of `<path>/log.md` (rare, pre-existing files) followed by the root log entries whose text starts with `[<path>] `, up to the overall `n` limit. The tool never silently returns an empty string: no entries → JSON note `{"entries": 0, "note": "..."}`.

b) **Size guard on `concept_read`**: body beyond `conceptReadSizeGuard` = 60000 bytes (`internal/mcpserver/tools_read.go`) with neither `section` nor `outline` → response as an outline (`{id, content_hash, outline: [{level, title, bytes}], body_bytes}`) plus a `note` explaining to use `section` or `full: true`. `full: true` always forces the full content. New helper `okf.ListHeadings` (same section semantics as `ExtractSection`) feeds both the outline and the "section not found" error, which now lists the available headings (cap 50) instead of leaving the agent to guess.

c) **`concept_oversize` lint** (`info` severity, like `map_oversize`): body beyond `conceptOversizeThreshold` = 30000 bytes (`internal/lint/lint.go`) flags the concept as a `concept_expand` candidate — the lint threshold is lower than the read one to anticipate the problem before it becomes a blocker.

**Rationale.** The three thresholds share the same principle: the KB must defend itself from its own growth instead of relying on the agent's discipline (same spirit as D77). The broken log-tail was a pure bug (asymmetry between `log_append`/`log_tail`), not a new design choice.

---

<a id="d88"></a>
## D88 — Frontmatter unset in `concept_patch`, `map_delete` tool

**Status: implemented (2026-07-24).**

**Context.** `applyFrontmatterMap` (`internal/mcpserver/tools_write.go`) only ever called `fm.Set(...)`; passing a JSON `null` value for a frontmatter key set it to a literal `nil` rather than removing it, even though the removal primitive already existed (`Frontmatter.Delete`, `internal/okf/frontmatter.go`). The bundled `kb-import` skill prescribes removing `status: imported` once a concept is curated — doable before this change only via a full `concept_write` rewrite. Separately, `map_create` scaffolds a Map/Journal directory but there was no inverse: an emptied map (all concepts moved out) required an out-of-band `rmdir`.

**Decision.**
- **WP1 — null-as-unset.** In `applyFrontmatterMap`, a JSON `null` value now calls `fm.Delete(key)` instead of `fm.Set(key, nil)`. No new protection is added for reserved/required keys: the existing `kb.WriteConcept` validation (`type field is required`) already rejects a write that ends up without a `type`, so deleting it fails the same way a `concept_write` without `type` always has.
- **WP2 — `map_delete(map)`.** New tool, git-wrapped like every other write. Deletes the map/journal directory **only if empty** — i.e. it holds nothing beyond the `map_create` scaffold (`_map.md`, `index.md`, `log.md`); if any concept remains (found via `WalkConcepts` under the map's prefix), the map is left untouched and the error lists the concepts so the caller moves them with `concept_move` first. Classified agent-visible (not advanced), same tier as `map_create`.

**Rationale.** Reusing the existing `Frontmatter.Delete` and `WriteConcept` validation means null-as-unset needed no new protected-key logic — the "type is required" invariant already did the job. Emptiness-only `map_delete` (rather than a recursive/forced delete) keeps map removal a safe, git-diffable no-op unless the caller has actually finished migrating content out, consistent with `concept_delete`/`concept_move` never silently discarding concepts.

---

<a id="d89"></a>
## D89 — Multi-term search fallback and coherent search modes

**Status: implemented (2026-07-24).**

**Decision.** FTS5 keyword queries tokenize on whitespace and first require all usable terms; an empty result retries once with any term. Tokens shorter than three characters are excluded from the trigram MATCH, preserving the prior whole-query behavior when no usable token remains. The in-memory index follows the same all-terms-then-any-term policy. Both search profiles expose `mode: keyword|semantic|hybrid`; `use_semantic=true` remains a deprecated alias for `mode=hybrid`.

**Rationale.** Requiring a whole multi-word trigram phrase was stricter than the in-memory all-terms behavior and missed documents containing the terms apart. The OR retry improves recall only when the more precise result is empty, while preserving ranking and ordinary keyword behavior. This unifies the public schema without changing the capability gate: as established by D43 and AD11, semantic/hybrid search still requires a server started with Ollama and keyword search remains available without it.

---

<a id="d90"></a>
## D90 — Content-hash reconciliation for derived search indexes

**Status: implemented (2026-07-24).**

**Context.** Incremental index updates previously occurred only through MCP writes. Imports, manual filesystem edits, and concepts pulled by git therefore left a non-empty SQLite index stale; startup only rebuilt when the table was empty, so one service restart did not repair drift.

**Decision.** SQLite stores one content hash per concept, so reconciliation walks the KB, compares its hashes with `AllHashes()`, and incrementally upserts new/changed concepts and deletes vanished ones. The in-memory index follows the same add/remove path as MCP writes. Reconciliation runs at boot, after a successful `SyncIn` that changes HEAD, and through the write-scoped `reindex` MCP tool. `cartographer reindex [--kb <name>]` calls a healthy configured server over HTTP; only while it is down does the local administrative CLI open `<kb>/.cartographer/index.db` directly.

**Rationale.** Hash reconciliation makes the index converge without an always-on watcher, preserves the server's single owner of a live SQLite connection, and avoids needless embedding work: changed hashes naturally miss the existing embedding cache until a semantic rebuild/search refreshes them. A filesystem watcher (`fsnotify`) was rejected for this release: it adds platform-specific lifecycle and event-loss complexity while still requiring reconciliation after imports, git pulls, and restarts.

---

<a id="d108"></a>
## D108 — On-demand backlinks and frontmatter facets

**Decision.** The link graph derives inbound and outbound adjacency on demand by
walking the KB with each concept's physical path. It is not persisted: vault
files remain the truth, including relative links inside expanded concepts, and
the modest walk avoids a second index reconciliation surface. `graph_neighbors`
selects `out`, `in`, or `both` direction on that same graph.

Structured predicates belong on `concept_list`, which inventories existing
concepts, rather than `search`, which ranks body text by relevance. Its small
frontmatter grammar intentionally supports only ANDed exact `key=value` and
`key!=value` predicates plus timestamp ranges. Regex and OR are absent: they
would make bounded inventory reads harder to predict without serving the
operational catalog questions this interface covers.

---

<a id="d122"></a>
## D122 — Bounded curated-index read/patch: `index_get(with_hash)` + `index_patch`

**Status: implemented (2026-08-04).**

**Context.** `index_get` was read-only and returned raw Markdown without a
content-hash, so an agent that found an incomplete curated index (D107's
`require_index_entry`/`index_incomplete`) could diagnose the gap but not
repair it through MCP. `concept_write`/`concept_new`/`concept_patch` all
reach `WriteConcept`, which rejects the reserved `index.md` name for the
direct-concept path; `artifact_write` deliberately excludes `data/**` (D106).
An expanded concept's own `index.md` (e.g. `map/concept`) is different: it
*is* a concept and was already writable through `concept_patch`.

**Decision.** `internal/kb` gains a bounded pair, `IndexHash`/`PatchIndex`,
resolved through `curatedIndexRelPath`: it accepts only the root `index.md`
and an existing Map/Journal's `index.md` (a real `_map.md`/`_archive.md`
descriptor must exist), rejects traversal, nested paths, non-index reserved
files and symlink components, and redirects a two-segment expanded-concept
index path to `concept_patch(id=<owner>)` with a typed `expanded_index`
error instead of silently misclassifying it. `PatchIndex` writes the
already-materialized replacement content atomically after an immediate
content-hash comparison against `ifMatch`, failing closed with
`ErrStaleWrite` and leaving the file untouched on any rejection.

At the MCP layer, `index_get` gained an opt-in `with_hash: true` returning
structured `{path, content, content_hash}`; the default stays byte-for-byte
raw Markdown for every existing caller. `index_patch` reuses `concept_patch`'s
Edit-tool semantics verbatim (single `old_string`/`new_string`/`replace_all`
or an atomic `edits` batch) applied to the curated index's full content
instead of a concept body, goes through `gitWrap` (one commit, one root
`log.md` entry), and never touches the live/SQLite concept search indexes —
curated indexes are not concepts. Authorization introduces a fifth resource
class, *curated index*: a Map/Journal path is authorized against that single
collection, the same as `concept_move`'s destination; the root index has no
Map/Journal of its own, so it requires the same unscoped whole-KB write grant
as the tools in that class, even under a policy that already grants a write
inside one specific Map.

**Rationale.** Bounded explicit curation was chosen over automatic index
generation: an index is deliberately curated prose (ordering, grouping,
prose framing an agent chooses), not a derived listing — `concept_list`
already covers the exhaustive/derived case. Reusing `concept_patch`'s exact
Edit-tool semantics and JSON shape keeps one mental model for "patch some
text under `if_match`" instead of inventing a second patch grammar for
indexes. A new resource class (rather than folding `index_patch` into
`resourceWhole` like `index_get`) lets a Map-scoped principal curate its own
Map's index without a whole-KB grant, while keeping the root index — which
has no bounded collection of its own — behind the same unscoped grant every
other whole-KB tool already requires.

---

<a id="d123"></a>
## D123 — Expose read-only governance tools to descriptor-bound agents

**Status: implemented (2026-08-04).**

**Context.** The default `agent` tools profile (D65) hid `validate`, `lint`,
`gate_check`, and `kb_status` from `tools/list`. They stayed registered and
directly callable by name (`ToolAdvanced` filters `tools/list`, not
dispatch), which is enough for a stdio/TUI agent that can invoke any known
tool name. It is not enough for a descriptor-bound MCP host such as Codex,
which can only invoke tools `tools/list` advertises: the documented agent
loop (`docs/loop.md` §Validate and lint, `docs/use-cases.md`) requires all
four, but a descriptor-bound agent had no way to call them.

**Decision.** Remove `validate`, `lint`, `gate_check`, and `kb_status` from
`advancedToolNames` (`internal/mcpserver/visibility.go`): they join the
`agent` profile's core set, unchanged in every other respect — same
`ReadOnly: true` classification (`readonly.go`), same `resourceWhole`
resource class and whole-KB RBAC (`policy.go`), same handlers. Everything
else stays advanced: `commit_gate`, contradiction/conflict mutation, index
maintenance, provisioning, secrets, artifact enumeration/removal, and PR
finalization remain operator-only, because they are either mutating or
genuinely niche — unlike these four, no documented agent-loop step depends
on a descriptor-bound host being able to invoke them without an operator
switching the server to `profile: full`.

**Measured cost.** Serialized `tools/list` under the `agent` profile against
the demo KB: 28 tools / 21814 bytes (~5453 tokens, bytes/4 estimate per D65)
before, 32 tools / 23324 bytes (~5831 tokens) after — a ~378-token, ~7%
increase for the four added descriptors, well short of erasing D65's
~2.9k-token reduction from the pre-D65 baseline.

**Rationale.** No third profile and no umbrella governance tool: a third
profile duplicates the two-value contract admin/operators already reason
about (`agent`/`full`) without resolving the descriptor-bound gap for the
existing default, and an umbrella tool (`kb_admin(action=...)`, already
discarded once by D65) trades four typed, independently discoverable
schemas for one opaque one. Reclassification also does not touch RBAC:
`ToolAdvanced` gates `tools/list` visibility only, `authorizeTool` and the
resource-class tables are untouched, so a caller who could not call these
four before still cannot call them after — only what a compliant
descriptor-bound client is told exists has changed.
