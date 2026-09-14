---
topic: control-plane
---

# D71 — MCP tools for provisioning artifacts: `artifact_read`/`artifact_write`/`artifact_list`/`artifact_delete`

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
