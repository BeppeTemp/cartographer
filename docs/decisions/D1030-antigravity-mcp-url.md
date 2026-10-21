---
topic: client-configurator
---

# D1030 — Antigravity emits canonical "url" key for MCP Streamable HTTP

**Decision.** `emitAntigravityServer` in `internal/configurator/configurator.go` emits
`"url": spec.URL` instead of `"serverUrl": spec.URL` for HTTP server entries in
`.gemini/config/mcp_config.json` (and `.agents/mcp_config.json`).

This amends D194 §MCP Configuration Emission, which emitted `"serverUrl"` for Antigravity.

**Why.** When Antigravity support was initially introduced in D194, Antigravity/Gemini CLI
used `"serverUrl"` for remote HTTP server entries. With the release of Antigravity's Streamable
HTTP transport support (aligned with the MCP Streamable HTTP 2025-03-26 specification),
Antigravity promoted `"url"` to the canonical and required key (<https://antigravity.google/docs/mcp>),
maintaining `"serverUrl"` solely as a backwards-compatible fallback.

Furthermore, across all other six providers supported by Cartographer (`claude`, `codex`, `copilot`,
`crush`, `kiro`, `opencode`), the emitters already generate `"url"`. Aligning Antigravity with `"url"`
ensures consistency across all provider emitters in Cartographer and adopts Antigravity's preferred
modern schema.

**Alternatives rejected.**
- *Emit both "url" and "serverUrl".* Antigravity prioritizes `"url"` and documents it as the required
  key; duplicate keys in user JSON configuration files are redundant and non-standard.
- *Keep "serverUrl".* Retaining legacy keys when the upstream client has modernized creates divergence
  and risks deprecation breakage in future Antigravity releases.

**Consequences.**
- `cartographer connect antigravity` and `cartographer sync` emit `"url"` in `mcp_config.json`.
- Non-destructive `mergeJSON` continues preserving pre-existing user configurations.
