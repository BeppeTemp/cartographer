---
topic: client-configurator
---

# D29 — OpenCode format aligned to the official schema + `kb.Init` creates the git repo

### D29a — OpenCode format: `opencode.json`, `$schema`, `enabled`, `command` array
**Decision.** `emitOpenCode` in `internal/configurator/configurator.go` is aligned to the official OpenCode v1.17.10 schema (`https://opencode.ai/config.json`):
- The generated file is `opencode.json` (not `.opencode/config.json`).
- The root key `"$schema": "https://opencode.ai/config.json"` is always included.
- Each MCP entry includes `"enabled": true`.
- For stdio transport: `"command"` is an **array of strings** (not a scalar string).
- For remote transport with auth: `"headers"` uses OpenCode's native `{env:VAR}` syntax (not `${VAR}`).
**Rationale.** The previous format was based on incomplete documentation. The real schema (verified against an OpenCode v1.17.10 config) requires a `command` array and an explicit `enabled`. The `{env:VAR}` syntax for env vars diverges from Claude/Kiro's (`${VAR}`) and must be documented explicitly to avoid misconfigurations. Known risk: OpenCode is SSE-first and custom header support on remote MCP may require `mcp-remote`/`mcp-auth.json` (see `interoperability.md`).

### D29b — `kb.Init` initializes the KB as a git repository (best-effort)
**Decision.** `kb.Init` runs `gitx.Init` + initial commit "init: KB inizializzata" after creating the layout, only if the directory is not already a git repo (`gitx.IsRepo`). The git init is **best-effort**: if git is unavailable or the init fails, Init does not fail — the KB remains valid without git. `WriteConcept` does not auto-commit: commits remain an explicit operation (`commit_gate`).
**Rationale.** `interoperability.md` states that "each KB is its own git repository", but `Init` did not initialize the repo, leaving the KB non-git unless a manual step was taken. This caused `ErrNothingToCommit` on the first `commit_gate` and broke tools that assumed a git repo (e.g. `sync_check`). Best-effort ensures that tests and environments without git installed are not broken.
