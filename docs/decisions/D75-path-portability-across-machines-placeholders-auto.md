---
topic: sync-provisioning
---

# D75 — Path portability across machines: placeholders auto-resolved via git remote

**Status: implemented (2026-07-10); the "never absolute paths" phrasing below is corrected by
[D124](D124-machine-path-distinguishes-client-local-paths-from-a.md)** — read "client-local absolute paths"; a Map-level allowlist now
distinguishes those from operational target paths that are identical on every reader's machine.
WP1–WP6 complete: `internal/repoindex` (Scan/Resolve, cache
`~/.config/cartographer/repos.json`), `search_roots`/`paths` in `.cartographer.yaml`, client-side
expansion in `provisioning.Apply` with hash on the expanded content, "Local paths" table in the
instructions block, `cartographer resolve`, `machine_path` lint. Details → `docs/sync.md`
§Path portability placeholders, `docs/configurator.md` (`resolve`, `.cartographer.yaml`).

**Deviations/clarifications versus the original spec:**

- `ApplyOptions` gains an explicit `ExpandPlaceholders bool` field (besides `SearchRoots`/
  `Paths`), not mentioned in the spec: it makes "the server never expands anything" a structural
  guarantee (the field stays `false` by construction in `internal/mcpserver`, never set there)
  instead of depending on the accidental absence of `SearchRoots`/`Paths` — it also prevents a
  server-side `Resolve` from attempting a `LoadCache`/`Scan` on the server's filesystem anyway.
- Intentional consequence of hash-on-expanded-content: for an artifact containing at least one
  placeholder, `ManagedFile.ContentHash` (client, expanded) never converges with `Artifact.ContentHash`
  (server, raw) — that artifact is therefore re-materialized at **every** `sync`, not only when
  the KB content really changes. It is not a bug: it guarantees that a repo re-cloned elsewhere (or an
  updated `search_roots`/`paths`) is reflected in the expanded path at the next sync, instead of
  staying frozen in the lockfile. No impact on artifacts without placeholders (zero drift,
  tested).

**Decision.** In shared content (concepts and provisioning artifacts) **never absolute paths**. Two placeholders:

- `{{repo:<name>}}` (short form) / `{{repo:<host>/<owner>/<name>}}` (full, canonical form) — resolved **automatically**: the key is the **normalized git remote**, identical on every team machine; the local path is discovered by scanning the filesystem. Zero manual mapping for the common case.
- `{{path:<nome>}}` — manual `paths:` mapping in `.cartographer.yaml`, fallback for directories that are not git repos.

No server-side substitution: it would break `content_hash`/`if_match` (hash on the raw, served content different per user) and the server does not know the clients' filesystems. All resolution is client-side.

**WP1 — `internal/repoindex`.** `Scan(roots []string)`: walk with depth cap (default 4) and skip of heavy dirs (`node_modules`, inner `.git`, caches); for each repo it reads `remote.origin.url` and normalizes it to `host/owner/name` (handles scp-like ssh, https, `.git` suffix). Cache in `~/.config/cartographer/repos.json` with refresh on-miss. `Resolve(key)`: `paths:` map → cache → rescan → not found. Ambiguity: short name with multiple distinct remotes → error asking for the full form; multiple clones of the same remote → deterministic root order, first match + warning.

**WP2 — clientconfig.** `search_roots:` fields (default `["~/Documents"]`) and `paths:` (name→path map) in `Config`/`yamlConfig` (`internal/clientconfig/clientconfig.go:20`). Never on git: the file is already per-machine.

**WP3 — expansion at materialization.** `provisioning.Apply` expands the placeholders in the textual contents before writing to disk (write points: `provisioning.go:1017`, `:1779`, instructions block `:1283`). The **lockfile hash is computed on the expanded content** — drift detection compares the disk, it must compare like with like. Unresolved placeholder → warning on stderr and text left as-is (the sync does not block).

**WP4 — "Local paths" table in the instructions block.** At materialization the client collects the placeholders present in the KB's artifacts, resolves them and appends to the instructions block a key→local path table, plus the instruction for the agent: placeholder encountered in a concept and absent from the table → `cartographer resolve <key>`.

**WP5 — `cartographer resolve` subcommand.** `cartographer resolve repo:<...>|path:<...>` prints the resolved path: a runtime fallback for the agent (the binary is already on every connected machine) and a debugging tool.

**WP6 — `machine_path` lint (warning, server-side).** Flags home-anchored paths in concept bodies: `/Users/`, `/home/`, `~/`, `C:\Users\`. Deliberately narrow pattern: absolute container/cluster paths (`/etc/...`, `/var/...`) are legitimate and identical everywhere. Refined by [D124](D124-machine-path-distinguishes-client-local-paths-from-a.md): a candidate inside a URL, or covered by a Map's `machine_path_allow_prefixes` allowlist, is a target-operational path rather than a client-local one and is not flagged.

**Rationale.** The git remote is the only identifier of a repo that is **already** shared and stable across the team's machines — using it as the key eliminates the one-to-one manual mapping that does not scale. Resolution lives in the client (scan+cache) and in the two channels the agent already has: the materialized imprinting (table) and the local binary (`resolve`). Discarded alternatives: pure manual mapping (does not scale, it was the v0 of this decision); server-side per-user profiles (identity and filesystem on the server side, hash breakage); resolution via env vars (explodes into N envs for N repos).
