---
topic: sync-provisioning
---

# D69 — `kind: mcp` provisioning: third-party MCP servers distributed by the KBs

**Status: active.** HTTP transport only (per the starting decision). WP1–WP4 and WP6
implemented as planned; WP5 (trust) implemented with a more restrictive security choice
than the plan implied (see below); the server-side allow-list
(optional in the plan) **not implemented**, deferred to Phase 3.

**Context.** KBs already distribute skills, agents, hooks, and instructions to clients via the
manifest→lockfile→apply flow (D27/D48/D56). A kind for **third-party MCP servers** is missing: today
the only MCP the client configures in the agents is Cartographer itself (`internal/configurator`,
`cartographer:mcp:*` blocks). All the necessary infrastructure already exists: the configurator can
emit MCP config for the 4 providers (for itself), `hooksettings.go` has the idempotent merge
+ prune patterns on the providers' config files. The feature is "generalize MCP emission and hook it
into the artifact flow". N.B.: the KB `mcp/` folder introduced here has **no** relation to the
`mcp/` removed by D28 (that was something else, June 2026).

**Starting decision.** HTTP transport only in this iteration. `stdio` implies referencing a
command/binary present on the client — more useful but thornier (distribution, paths, security);
it will be added later (the `type` field is already in the schema for that day).

**WP1 — Source format in the KB.** `mcp/` folder in the KB, one JSON file per server:
`mcp/<nome>.json` (single-file like agents, not a directory like skills). Provider-neutral
schema: `{"type": "http", "url", "headers", "env"}`. **Security constraint**: no
secrets in the file — the values of `headers`/`env` support only `${VAR}` references resolved
from the client's environment (`token_env` pattern, D64); `parseMCPServerSpec` rejects a value with
no `${VAR}` reference at all (it looks like a literal secret), a `type` other than `"http"`, or a
missing `url`. `env` is validated with the same rule for future `stdio` compatibility, but it is
not yet emitted by any provider (see WP3): none of the 4 currently exposes a verified channel for
generic env vars on an "http" server — only the Authorization header can be represented
reliably.

**WP2 — BuildManifest.** New step in `BuildManifest` (provisioning.go): scan of `mcp/*.json`
for each KB → an `Artifact{Kind: "mcp", Source: "kb:<nome>"}` with `contentHashFile` (like
agents). Missing folder → zero artifacts (backward compat). Schema parse+validation happens here, so
a malformed file or one with a literal secret fails the build, not the apply.

**WP3 — Apply per provider.** Unlike skills/hooks, an MCP server does not materialize its own
files: it **merges into the provider's native config** (`internal/provisioning/mcpsettings.go`,
`registerMCPServer`/`removeMCPServer`):
- claude: key `mcpServers.<nome>` in `~/.claude.json`;
- codex: block `[mcp_servers.<nome>]` in `.codex/config.toml` with markers
  `# cartographer:mcp:<nome>:begin/end` (pattern from `registerHookConfigTOML`, distinct from the
  unnamed `cartographer:mcp:begin/end` block that `internal/configurator` writes for the
  Cartographer entry itself via `connect` — no collision);
- opencode: key `mcp.<nome>` in `opencode.json`;
- kiro: `mcpServers.<nome>` in `.kiro/settings/mcp.json`.

Key refactor: `internal/configurator.EmitServer(name, spec ServerSpec, provider)` extracted from
`Emit`/`ServerConfig` (which is now a thin wrapper over `EmitServer(cfg.Name, cfg.toSpec(),
provider)`), used both by `connect` (for the Cartographer entry) and by `provisioning.Apply` (for the
KBs' servers). Each `${VAR}` reference is translated per provider: claude/kiro/codex leave it
verbatim, OpenCode translates it to `{env:VAR}`. Native limits not worked around with heuristics: Codex
exposes only `bearer_token_env_var` (an `Authorization: Bearer ${VAR}` header translates to it, every
other header is dropped with a warning in `EmitResult.Warnings`); Kiro never had a header
field for MCP servers (pre-existing limit, not introduced here) — a KB server with headers
generates a warning in `AppliedResult.Warnings`, not an error. Invariant preserved: only
own keys/blocks are managed, never the rest of the file (existing `connect` goldens unchanged).

**WP4 — Prune and disconnect.** `ManagedFile{Kind: "mcp"}` in the lock for each written server;
`PruneManaged` removes the single key/block (`removeMCPServer`, analogous to
`removeHookEntries`/`removeHookConfigTOML`), never the whole file — with the same empty-shell
cleanup as `configurator.Remove` (D63) for kiro/opencode. Round-trip test in
`provisioning_disconnect_test.go` extended: connect with a KB carrying an MCP server → disconnect →
clean provider configs (`TestRoundTrip_ConnectDisconnect_NessunResiduo`).

**WP5 — Trust and remote sync.** An MCP server is an endpoint that receives the agent's data:
Before D114/D115, `BuildManifest` marked the `mcp` kind **always** `Signed:false`, regardless of `autoTrust` — unlike
skill/agent/hook/instructions, which `autoTrust` signs. The remote client likewise excluded `mcp`
from its generic upgrade via `cfg.Trust`/`--auto-trust`: a stricter policy than the other kinds,
`NeedsApproval` at first appearance and at every hash change, even with `AutoTrust` active.
**Deviation from the plan, since resolved**: this iteration had no mechanism to mark a single
`mcp` artifact as approved — the only generic gate was `cfg.Trust`/`--auto-trust`, which `mcp`
ignores by construction. The persisted point approval (of what exactly, for which hash) arrived
with D115 as `cfg.MCPApprovals`; the client-side upgrade hook it describes no longer exists,
since D114 made `Signed` an exclusively cryptographic result. `sync_pull`/`tools_sync.go` and the HTTP client do not filter by kind (verified):
an `mcp` artifact travels as a single `ArtifactFile`, same schema as an agent.

**Server-side allow-list**: **not implemented** (optional in the plan, tied to the Phase 3
"MCP registry allow-list" item below).

**WP6 — Documentation and closure.** `docs/sync.md` §MCP servers, `docs/configurator.md`, this
entry and the release tracking state then in use. Tests:
`internal/provisioning/mcpspec_test.go` (schema validation),
`internal/configurator/configurator_mcpserver_test.go` (`EmitServer` goldens for the 4 providers),
`internal/provisioning/provisioning_mcp_test.go` (BuildManifest/Apply/Prune), extended round-trip in
`provisioning_disconnect_test.go`.
