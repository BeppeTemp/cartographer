# Roadmap and status — Cartographer

The only place where the project's **progress status** lives (CLAUDE.md is stable imprinting and does not replicate it). The detail of each choice → `decisions.md`; the current version → git tag.

## Completed

### Phase 0 — Local Core (v1)

- **M1 — Foundation**: Go scaffold, OKF primitives (`internal/okf`), KB data plane (`internal/kb`), git wrapper (`internal/gitx`), hand-rolled stdio MCP server, read tools.
- **M2 — Write path**: structured frontmatter, `ContentHash`/`SectionHashes`, write tools with `if_match`, `validate`.
- **M3 — Search**: pure-Go inverted index, `search`/`index_rebuild`, hierarchical and graph navigation.
- **M4 — Governance loop**: `lint`, `commit_gate` (`source_ingest`/`scrub` later removed, D28).
- **M5 — Agent contract + end-to-end loop**: `gate_check`, complete e2e test (the generated `AGENTS.md` was later replaced by the `instructions` kind, D56/D62).

### Phase 1 — Server profile

Streamable HTTP transport (`POST /mcp`, `/health`, RFC 9728), bearer token + scope (`internal/auth`), multi-KB with `?kb=` routing, complete `internal/gitx`, semantic search prepared (`internal/embed`), hash-chain audit log (`internal/audit`).

### Phase 2 — Skills, services, secrets

`internal/skill` (SKILL.md loader/validator), `internal/sops` (encrypted secrets), Dockerfile (the local docker-compose of this era was later removed, D73).

### Post-1.0 extensions

- **Transactional git** Steps 1–4: 1 commit per write, remote sync fetch/rebase/push, conflict registry + `degraded` markers + guided skill, `git_conflict_resolve` record→finalize tool (D30–D33).
- **Full semantic search**: FTS5 trigram + embedding cache in SQLite (`internal/sqlindex`), best-effort with in-memory fallback (D32, D43).
- **Client synchronization** Layers 1–3: manifest+revision, multi-provider lockfile v2, drift detection, `SessionStart` bootstrap hook, `sync_check`/`sync_apply`/`sync_pull` tools, managed-only prune (D27, D34, D40 — see `sync.md`).
- **Agent-level E2E testing**: headless OpenCode harness, `test/e2e/` (see `testing.md` §Level 5).
- **M6 — Unified client + CI/CD** (2026-07): single subcommand binary + TUI dashboard (D35/D37), server YAML config (D38), KB bootstrap from a git remote (D39), remote HTTP client with client-side trust (D40), Gitea Actions release+deploy pipeline (since migrated to GitHub Actions, D79) + `install.sh` (D41).
- **M7 — Real multi-KB** (2026-07): per-KB scoped token with r/rw enforcement (D44/D45), per-KB git and SOPS identity (D46/D47), `kind: agent`/`hook` provisioning (D48), interactive `connect` (D49), `kb-create` GitOps skill.
- **Post-M7 — Imprinting and provider integration** (2026-07): convention-based config (D53), persisted trust (D54), agent on OpenCode/Codex (D55/D58), `instructions` kind + curated `instructions.md` (D56/D61), hooks auto-registered on claude/codex/opencode (D57–D59), client-side bootstrap hook (D60), clean KB repos (D62), disconnect with no leftovers (D63), connect UX (D64), default `agent` tool profile + compact instructions (D65), D65 rollout to production + curated `instructions.md` condensation.
- **Control-plane fix** (2026-07): `kb_overview` counts concepts instead of subdirectories (D66, fixes the false "empty KB" on flat KBs); `concept_delete` tool to remove a concept from the control plane (D67). Both require a server redeploy.
- **D70 — Incremental update ergonomics**: `concept_patch` tool (old_string/new_string with `if_match`, string-replace on the body only) and `title`+`snippet` in `search` results (in-memory, native FTS5, hybrid) — avoids full-rewrite and `concept_read` to gauge the relevance of a hit.
- **`kind: mcp` provisioning** (2026-07): third-party MCP servers distributed from KBs (`mcp/<name>.json`) and configured in providers via the client, merged into each one's native config (`.claude.json`/`opencode.json`/`config.toml`/`mcp.json`) — HTTP transport only in this iteration; `internal/configurator.EmitServer` extracted as a provider-neutral core reused by `connect`; stricter trust than the other kinds (never auto-signed) (D69).
- **D71 — Provisioning artifact tools** (2026-07-09): `artifact_read`/`artifact_write`/`artifact_list`/`artifact_delete` with path whitelist, per-file sha256 `if_match`, per-kind validation and per-KB `allow_artifact_write` flag (default off). An MCP-only agent can self-maintain the skills/agents/hooks/mcp/instructions of its own KB.
- **D72 — KB refactoring ergonomics** (2026-07-09): first-class `[[id]]` wiki-links in `ExtractLinks` (graph/lint see the real links); atomic batch `concept_move` with server-side backlink rewrite and updated indices (fixes stale FTS entries); `concept_list` tool for inventory; `index.md` stub on implicit expanded concepts + enforced max depth + `dossier_missing_index` lint. `homelab-wiki` remediation post-deploy.
- **D73 — Native local mode** (2026-07-09): `cartographer service` subcommand (`internal/service`): the server as a launchd/systemd user service, config generated in `~/.config/cartographer/server.yaml`, loopback bind by default; `serve` HTTP tolerates an empty data dir (0 KBs, no crash-loop); `connect` sugar (install proposal on a failed loopback probe) and automatic service restart on `install.sh update`; `docker-compose.yml` removed (Docker remains only as a CI artifact for k8s deploy).
- **D74 — Import of external non-OKF wikis/KBs** (2026-07-10): bundled `kb-import` skill (incremental agentic curation) + `cartographer import` CLI scaffold (archive/expanded-concept mapping per directory, frontmatter synthesis/completion, best-effort rewrite of relative markdown links, wiki-links left unchanged) + `imported_draft` lint (warning on `status: imported`) as a resumable curation backlog. Detail and WP in `decisions.md` §D74.
- **D75 — Multi-machine path portability** (2026-07-10): `internal/repoindex` (client-side scan+cache of git remotes, auto-resolved `{{repo:…}}`), manual `{{path:…}}` in `.cartographer.yaml` as fallback/override, client-side expansion at materialization (`provisioning.Apply`, hash on the expanded content, zero drift with no placeholder) + "Local paths" table in the instructions block, `cartographer resolve` subcommand, `machine_path` lint (warning). Detail and WP in `decisions.md` §D75.
- **D76 — Write-path latency** (2026-07-10): `concept_patch` accepts an atomic, sequential `edits[]` batch (1 call = 1 commit); `tools/list` exposes `annotations.readOnlyHint` for `[R]` tools; freshness window on `SyncIn` (`git.in_window`, default 30s); debounced async per-KB `SyncOut` (`git.out_debounce`, default 3s, `0` = rollback to synchronous push), worker under the same git lock, conflicts in the existing registry/degraded flow (D31), flush on sync-sensitive tools and at shutdown (graceful SIGINT/SIGTERM on the HTTP side); per-phase telemetry on stderr. Async transport, local commit unchanged (D30). Detail and WP in `decisions.md` §D76.
- **D77 — Atlas/Map/Journal hierarchy** (2026-07-10): new lexicon (Atlas = KB, Map = mixed-type thematic archive, Journal = chronological log); the dossier disappears as a level and becomes a concept state (*expanded concept*) with ID-preserving `concept_expand` (`<id>.md` → `<id>/index.md` resolution, zero backlink rewrite); `_map.md` descriptor with `kind` (read-compat `_archive.md`); breaking MCP v2 surface (`atlas_overview`, `map_list`, `map_create(kind)`, `dossier_*` removed) → v2.0.0 release; lint guardrails (`expanded_as_category`, `map_oversize`, `expanded_ambiguous`, `legacy_archive_descriptor`, `expanded_missing_index`). `homelab-wiki` migration performed and verified. Detail in `decisions.md` §D77.
- **D78 — Robust reads on large concepts** (2026-07-14): `log_tail(path)` reads the root-log entries with `[<path>] ` prefix (was silently empty); size guard on `concept_read` (outline beyond 60 KB, `section`/`full` escape hatches); `concept_oversize` lint. Detail in `decisions.md` §D78.
- **D79 — Open source release** (2026-07-18): Apache-2.0, public repo `github.com/BeppeTemp/cartographer` (squashed initial commit, protected `main`: PR + `test` check, squash-only, no admin bypass), GitHub Actions pipeline (release-please + GoReleaser: binaries, Homebrew cask on `BeppeTemp/homebrew-tap`, `ghcr.io/beppetemp/cartographer`), docs and code fully in English, Gitea archived, homelab switched to ghcr. First public release `v2.2.0`. Detail in `decisions.md` §D79.
- **D80 — Versioning reset to 0.x beta** (2026-07-18): the v2.x line inherited from internal development is retired (it also broke Go tooling: no `/v2` module-path suffix, so `v2.2.0` was invisible to `go install`); public versioning restarts at `v0.1.0` with pre-major semver (`bump-minor-pre-major`: breaking → minor), beta disclaimer in README, pre-1.0 policy in CONTRIBUTING. Detail in `decisions.md` §D80.

## Known bugs

None open.

## Future phases

→ `decisions.md` §Future extensions and risks for the detail.

- **Phase 3**: granular RBAC scope enforcement; permission-aware retrieval; MCP registry allow-list + per-artifact approval for `mcp` artifacts (natural hook: D69's WP5); 2026-07-28 MCP features; post-quantum age identity; dynamic secret manager.
- **Server git profile**: working branch + PR for conflict resolution.

## Advancement rules

- Coding delegated to `dev`, coordinator verification (`make vet && make test` green).
- Update `decisions.md` on every architectural or implementation choice; this file on every milestone.
