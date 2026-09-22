---
topic: sync-provisioning
---

# D58 — Real Codex CLI integration: managed-block `config.toml`, TOML agents, hook engine

*(The hook registration part is superseded by [D230](D230-codex-hooks-register-in-hooks-json.md): Codex hooks now register in `hooks.json`; the MCP block and TOML agents are unchanged.)*

**The bug.** Since D23, `emitCodex` wrote `.codex/config.json` Claude Code-style — but Codex CLI
never reads that file: the only configuration it consults is `~/.codex/config.toml`, section
`[mcp_servers.<id>]`. The MCP integration with Codex never worked in practice.

**Decision.** Three extensions, verified against the official Codex docs:
- **MCP → managed-block `config.toml`.** `config.toml` is hand-curated (comments, order): never
  parsed/reserialized as generic TOML. `emitCodex` generates only the body of the
  `[mcp_servers.cartographer]` block; `Apply`/`Remove` materialize it with the new generic
  `internal/blocktext` package (`Write`/`Remove`/`ReplaceBetween` on text markers) inside dedicated
  markers. `Remove` also cleans up any legacy pre-D58 `.codex/config.json`.
- **Agent → `.codex/agents/<nome>.toml`.** `destDir` maps `agent`×`codex` to this path;
  `translateAgentForCodex` (parallel to D55) extracts `description` and puts the body in
  `developer_instructions`. Fields with no reliable equivalent (`tools`, `model`) omitted, same
  policy as D55: never guess a value.
- **Hook → materialization + hooks engine.** `destDir` maps `hook`×`codex` to
  `.codex/hooks/<nome>/`; registration goes inside the managed block of `config.toml` as an array
  of TOML tables, with a per-hook marker (`# cartographer:hook:<nome>:begin/end`) — same
  ownership-per-block principle as D57, here per-marker in text instead of per-substring in JSON.
**Rationale.** `internal/blocktext` factors out a problem that now recurs twice in
`config.toml` (MCP entry + N hooks); the pre-existing version for `instructions` (D56, HTML markers)
remains unrefactored — stable code, no reason to touch it alongside an unrelated
change.
**Open question.** The `hook.json` `matcher` is written for Claude Code (free substring);
Codex interprets it as a regex — no automatic conversion planned.
Details: `docs/sync.md` §Agents and hooks.
**Follow-up (July 2026).** The TUI dashboard (`mcpConfigStatus` in `cmd/cartographer/tui.go`)
checked for the MCP entry's presence with a single `json.Unmarshal`, still calibrated on the old
`config.json`: for Codex the file is TOML, so the parse always failed and the `mcp-config` badge
showed `missing` even with a correct config. Now, for providers with a `FilePath` ending in
`.toml`, the check looks for the `[mcp_servers.<name>]` table instead of parsing JSON.
