# Control Plane — Go server and MCP tools

## Rationale

**Go**: static binary, performant on I/O across many files, containerizable. **MCP**: an abstraction layer that keeps the system agnostic to the agent (Claude Code, Codex, Kiro, OpenCode all speak the same protocol).

## A (nearly) stateless server

For each mounted KB, the server keeps only a **rebuildable derived index** and a parsing cache. Everything else lives in the files. Multiple KBs per instance: each with its own index, lock, and git repo.

## Read/write boundary

- Concepts → writes go **only through tools**, with validation (frontmatter, `type`, provenance, OKF, map ontology).
- Updates → prefer **appending to `# History`**; supersession replaces content non-destructively.
- Every write → an entry in `log.md`; with `CARTOGRAPHER_GIT_AUTOCOMMIT=true` (default), it also produces a **per-operation git commit** (author `cartographer <cartographer@localhost>`).
- KBs are exposed as MCP Resources for **reads**; **writes** are gated MCP Tools.

**Commit per logical operation (Step 1 — local commit)**: every write tool (`concept_write`, `concept_patch`, `map_create`, `map_delete`, `concept_expand`, `log_append`, `snapshot`, `supersede`, `concept_move`, `concept_delete`, `conflict_resolve`, `skill_install`) is wrapped by `gitWrap`, which acquires the per-KB mutex, runs the tool, and on success (no application error) calls `CommitOp`. A failed commit does not turn a successful operation into an error: it is logged to stderr. `AutoCommit=false` (the struct's zero value) leaves everything unchanged and keeps compatibility with existing tests.

## MCP API

This list is the **source of truth** for the active tools (do not duplicate counts elsewhere).

Tools marked **[R]** have `Tool.ReadOnly=true` (`internal/mcpserver`): they never mutate KB content and remain callable with a `kb:<name>:r` scope. All others require `rw`. Enforcement and scope format → [`transport-auth.md`](transport-auth.md) §Per-KB authorization. The same `[R]` tools expose `annotations: {"readOnlyHint": true}` in `tools/list` per the MCP spec (D76): a client can use this to auto-approve reads without a manual allowlist, without having to derive the list by hand.

Tools marked **[A]** (advanced, `advancedToolNames` in `internal/mcpserver/visibility.go`) are **hidden from `tools/list` in the default `agent` tool profile** (D65): governance/maintenance and provisioning plumbing that would bloat the LLM agent's context without being useful in a normal session. They all remain **callable via `tools/call`** by name (CLI client, hooks, operator) in both profiles. Profile: `tools.profile` in YAML / `CARTOGRAPHER_TOOLS_PROFILE` / `--tools-profile`, values `agent` (default) \| `full` → [`deployment.md`](deployment.md).

### Reading and navigation

| Tool | Purpose |
|---|---|
| `atlas_overview()` **[R]** | Root index + maps/journals (concept count, and expanded concept count if any). |
| `map_list()` **[R]** | Lists maps and journals with metadata (`kind`, `ontology_mode`, `concept_types`, expanded concept count). |
| `index_get(path)` **[R]** | Reads a folder's `index.md` (progressive disclosure). |
| `concept_read(id, [section], [outline], [full])` **[R]** | Reads a concept or a single section (bounded). Returns the content-hash for `if_match`. A `section` not found → error with the list of available headings (capped at 50), no guessing. `outline=true` returns only the structure (`{level, title, bytes}` per heading) with no content. Size guard: a body over 60 KB without `section` or `outline` → returned as `outline` plus a `note` (no content), unless `full=true` forces the full content (D78). |
| `log_tail(path, [n])` **[R]** | Latest N entries relevant to `path`. Empty `path` = the verbatim root log. A non-empty `path` has no `log.md` of its own (see `log_append`): it filters root entries prefixed `[<path>] `, preceded by any entries of a pre-existing `<path>/log.md`. No entries → a JSON note `{"entries": 0, "note": "..."}`, never a silent empty string (D78). |
| `graph_neighbors(id, [depth])` **[R]** | Graph neighbors (used for lint scoping). The graph sees both markdown links `[text](rel.md)` (relative to the file) and wiki-links `[[id]]`/`[[id#section]]` (root-relative, D72 WP0). |
| `concept_list([scope], [limit])` **[R]** | Exhaustive inventory: `{id, title, type}` for every concept under the `scope` prefix (empty = the whole KB, including `services/`), ordered by id. `limit` defaults to 500, with `truncated`/`total` if exceeded. An exhaustive alternative to `index_get`'s progressive disclosure (D72 WP3). |

### Search

| Tool | Purpose |
|---|---|
| `search(query, [scope], [mode])` **[R]** | Keyword, semantic, or hybrid (keyword + vector) search. `mode` is `keyword` (default), `semantic`, or `hybrid`; semantic/hybrid require Ollama to be configured. `use_semantic=true` remains a deprecated alias for `mode=hybrid`. Keyword matching first requires all query terms, then retries with any term if that returns no hits. Every hit includes `title` (from the frontmatter) and `snippet` (an excerpt of ~200 chars around the match; FTS5 uses its native `snippet()`, otherwise it's extracted in-memory) — avoids a `concept_read` just to judge a hit's relevance (D70). |
| `index_rebuild()` **[R]** **[A]** | Rebuilds the keyword index (in-memory and FTS5 if present) and the embeddings if Ollama is active (with a content-hash cache). Read-only: it mutates only the derived/gitignored index, never KB content (D45). |

### Writing and ingest

| Tool | Purpose |
|---|---|
| `map_create(name, title, [kind], [concept_types], [ontology_mode])` | Creates a map (`kind: map`, default) or a journal (`kind: journal`): a directory with `_map.md`, `index.md`, `log.md`. |
| `map_delete(map)` | Deletes a map/journal directory, but only if it holds nothing beyond the `map_create` scaffold (`_map.md`, `index.md`, `log.md`); if any concept remains, errors listing them — move them out with `concept_move` first, then retry (D88). |
| `concept_expand(id)` | Promotes a concept to an expanded concept: `map/name.md` → `map/name/index.md`, **same ConceptID** (no backlink rewrite), from which it can grow with `map/name/child` satellites. Requires a 2-segment id; errors `not_found` / `already_expanded`. No inverse operation (D77). |
| `concept_write(id, frontmatter, body, [mode], if_match)` | Creates/updates with validation. `if_match` = expected content-hash; fails with `stale_write` if changed. Automatically updates the in-memory keyword index and, if present, the persisted FTS5 index (no `index_rebuild` needed; embeddings remain `index_rebuild`'s job). |
| `concept_patch(id, old_string, new_string, [replace_all], if_match, [frontmatter])` | String-replace patch on the body only (Edit-like semantics), without rewriting the entire concept. `if_match` is **mandatory**: fails with `stale_write` if changed. Fails with `old_string_not_found` or `old_string_ambiguous` (use `replace_all` for multiple matches). `frontmatter`, if present, is shallow-merged onto the existing frontmatter; a key set to `null` is removed rather than set to a literal null (fails if the key is required, e.g. `type`, D88). Same write path (indices, commit) as `concept_write` (D70). As an alternative to the `old_string`/`new_string`/`replace_all` triple, it accepts an `edits: [{old_string, new_string, replace_all?}]` field to apply several patches in a single call (one commit): the two forms are mutually exclusive; edits are applied **in order, atomically** (edit i+1 sees the result of edit i) — if one fails, nothing is written and the error reports the index of the failed edit (D76). |
| `log_append(entry, [path])` | Appends an entry to the **root** log (never a per-directory log). With `path`, the entry is prefixed `[<path>] ` and still written to root; `log_tail(path)` retrieves it by filtering on that prefix (D78). |
| `snapshot([message])` | Records an entry in `log.md`. With git auto-commit enabled, it also creates a git commit of the entire KB. |
| `supersede(source_id, target_id, [reason])` | Marks a concept as superseded by another. |
| `concept_move(source_id, target_id \| moves[], [rewrite_links])` | Moves one or more concepts (batch `moves: [{source_id, target_id}]`, single form kept for backward compatibility; the two forms cannot be mixed). Validates the entire batch before applying any move (application-level atomicity), one git commit per call. With `rewrite_links` (default `true`) it rewrites backlinks across the whole KB — wiki-links `[[old]]`/`[[old#section]]` and relative markdown links — in a single pass using the old→new map, and updates the indices (in-memory + FTS5) for both the moved and rewritten concepts; the result lists the applied moves and rewritten concepts. With `rewrite_links=false` backlinks are left intact (warning in the result, use `lint`). D72 WP1/WP2. |
| `concept_delete(id, [if_match])` | Permanently removes a concept from the KB (git commit). Incoming backlinks are not updated — use `lint` to find them. |

### Governance

| Tool | Purpose |
|---|---|
| `validate(scope)` **[R]** **[A]** | OKF compliance (frontmatter, `type`, reserved files). |
| `lint(scope, [mode])` **[R]** **[A]** | `mode`: scoped (delta + neighbors) \| full \| deep (cross-model). |
| `commit_gate()` **[A]** | Blocks when open `Contradiction`s are involved in the diff. |
| `gate_check()` **[R]** **[A]** | Combines validate + lint + commit_gate in a single tool (lightweight local gate). |
| `conflict_resolve(contradiction_id, resolution, [reason])` **[A]** | Closes an open `Contradiction`. |
| `contradiction_report([scope], [status])` **[A]** | Lists contradictions, filterable by scope and status. |
| `kb_status()` **[R]** **[A]** | Aggregate metrics: total concepts, per type, stale ones, open contradictions. |
| `conflicts_list()` **[R]** | Lists open git rebase conflicts (read-only). For each entry: `concept_id`, local/remote SHAs, `branch`, files involved, `detected_at`, resolution guidance. See also the `kb-conflict-resolve` skill. |
| `git_conflict_resolve(concept_id, strategy, [body])` | Resolves a registered conflict (Step 4). `strategy`: `ours` (local version), `theirs` (remote version), `edit` (full content in `body`). Records the per-concept decision; once every open conflict is resolved, it performs a single merge+commit+push and clears the `degraded` markers. See `concurrency.md` §Step 4. |

### Skills and Services

| Tool | Purpose |
|---|---|
| `skill_list()` **[R]** **[A]** | Lists the installed skills (`skills/`) and the ones bundled in the binary. Field `source`: `[installed]` \| `[bundled]`. |
| `skill_install(name, force)` **[A]** | Copies a bundled skill into `kb.Root/skills/<name>/`. Errors if already present; `force=true` overwrites. |
| `service_get(service_id, resolve_secrets=false)` **[R]** **[A]** | Reads a concept of type Service. With `resolve_secrets: true` it also decrypts the `secrets_source` (flat — the service's entire SOPS file; per-ref `secret_refs` cannot be parsed from the frontmatter, → `skills-services-secrets.md`) and includes it in the result; requires a KB with an age key configured and **rw scope** (enforced in the HTTP guard, not in the per-tool-name classification: `service_get` remains `ReadOnly` for the no-resolve path). |
| `service_list()` **[R]** **[A]** | Lists all concepts of type Service. |

### Client ↔ provisioning synchronization

| Tool | Purpose |
|---|---|
| `sync_check([applied_revision])` **[R]** **[A]** | Read-only. Returns the current manifest's `revision` (bundle + KB), the artifact list (`kind`: `skill`/`agent`/`hook`/`instructions`, `name`, `source`, `signed`), `in_sync=true/false` (if the client's lockfile revision is supplied), and **`open_conflicts`** (the count of open git rebase conflicts — useful as a SessionStart hook). Safe even on a remote server. |
| `sync_apply(base_dir, [dry_run], [auto_trust])` **[A]** | Materializes into `base_dir` the artifacts with `signed=true`, prunes obsolete managed ones, and updates the lockfile (`.cartographer-sync.lock.json`). Intended for local (stdio) deployments where server and client share the filesystem. Unsigned artifacts go into `needs_approval`. `dry_run=true` shows the diff without writing. `auto_trust=true` also treats KB skills as trusted (opt-in workspace policy, a placeholder for future signature verification). |
| `sync_pull()` **[R]** **[A]** | Read-only, no parameters. Returns the provisioning manifest with each artifact's file contents embedded in base64, for a remote HTTP client that does not share the filesystem with the server. Used by `cartographer connect`/`cartographer sync`/`cartographer status` on the client side (the `auto_trust` trust decision is client-side, not a tool parameter). |

### Provisioning artifacts (D71)

| Tool | Purpose |
|---|---|
| `artifact_read(path)` **[R]** | Reads an artifact file (`skills/<slug>/**`, `agents/<slug>.md`, `hooks/**`, `mcp/<slug>.json`, `instructions.md`) and returns its content + `sha256` (the `if_match` for `artifact_write`). |
| `artifact_list()` **[R]** **[A]** | Lists artifacts by kind, with their files and sha256 (classification reused from `provisioning.BuildManifest`; for `instructions` it reads the raw `instructions.md` file, not the generated content). |
| `artifact_write(path, content, [if_match])` | Creates/updates an artifact file. On an existing file, `if_match` (sha256) is **mandatory** (`already_exists` if missing, `stale_write` if wrong); per-kind validation before the write (SKILL.md → `skill.Validate`; agent → `name`+`description` frontmatter; mcp → the D69 descriptor; capped at 256 KiB). Only registered if the KB has `allow_artifact_write: true` (default off — writing a skill means injecting instructions that clients will execute). |
| `artifact_delete(path, if_match)` **[A]** | Removes the file (and the directory if left empty). Same per-KB flag as `artifact_write`. |

`artifact_write`/`artifact_delete` go through `gitWrap` (lock, commit, sync) and notify `skills/list_changed` when the path is under `skills/`. The per-KB flag → [`deployment.md`](deployment.md).

See `docs/sync.md` for the full model (Manifest, Lock, Diff, layered triggers).

> Multi-KB: every tool that operates on content accepts a `kb` parameter (omittable if there is only one KB).

## Semantic search

Semantic search is available when the server is started with `--ollama <url>` (or `CARTOGRAPHER_OLLAMA=<url>`). In that case:

- The `search` tool accepts `mode=semantic` or `mode=hybrid` to use vector similarity; `use_semantic=true` remains a deprecated alias for `mode=hybrid`.
- The `index_rebuild` tool rebuilds the keyword index and per-concept embeddings.
- The model is configurable via `CARTOGRAPHER_OLLAMA_MODEL` (default: `nomic-embed-text`).
- If Ollama is unreachable or embedding fails, keyword search is always available as a fallback.

## Search index

**Rebuildable index** (*vault = truth, index = disposable*), with two persistence levels:

- **In-memory (default/Core)**: a pure-Go keyword inverted index (`internal/search`) + an in-memory vector store (`internal/embed`). Rebuilt on every startup by walking the concepts.
- **Persisted SQLite (`internal/sqlindex`, D32)**: when the KB has an openable `.cartographer/index.db`, keyword search uses **FTS5 with a trigram tokenizer** (supports substrings, not just whole words). Multi-term matching tries all terms first, then any term only if the all-terms search is empty; terms shorter than three characters are omitted from the FTS match. Embeddings are persisted in SQLite with a **content-hash cache** — `index_rebuild` recomputes Ollama embeddings only for concepts whose hash changed. On this path, the `search` response's `mode` field is `keyword_fts5`/`hybrid_fts5`. Best-effort: if the DB can't be opened or FTS5 is unavailable, it degrades to the in-memory path.
- **Semantic**: embedding vectors (e.g. Ollama), cosine similarity outside SQL. Independent commit: if the embedder is down, keyword search still moves forward. Embedder-identity guard: full re-embedding if the model changes (content-hash invalidates the cache).

At small scale `index.md` alone can be enough, and keyword search beats embeddings on cost; semantic search is nonetheless active from the start in the Server profile and scales with the wiki.

## Validation and invariants

- **OKF compliance** (`validate`): parseable frontmatter, non-empty `type`, well-formed reserved files.
- **Project invariants**: `provenance` on derived concepts; `type` allowed by the palette if `ontology_mode: strict`.
- **Contradictions — hybrid model:**
  - **SOFT / scope-mismatch** → typed edges (`contradicts`/`tension`) emitted by lint, non-blocking.
  - **HARD** → a first-class `type: Contradiction` node with `resolution_status`.
  - **Deterministic commit gate**: `grep` for `resolution_status: open` on `Contradiction` nodes that `involves` a concept touched by the diff — near-zero model cost, blocks/escalates.
- **Broken-link tolerance**: informative stubs, not errors.

## Provenance, citations, audit

Every concept is traceable to its raw sources (`provenance` + `# Citations`). `log.md` = chronological audit trail with agent identity. **git** = full history. The server also maintains its own **proprietary audit log** (append-only JSONL with hash-chain): server, tool, args, outcome, timing, commit-sha.

The content-hash is computed **per-section** (as well as per-file) on normalized content (UTF-8/LF, sorted YAML keys, excluding auto-generated fields like `timestamp`) to avoid spurious `stale_write`s. Appends to `# History`/`log.md` use an **idempotent** primitive that does not require `if_match` on the whole file.
