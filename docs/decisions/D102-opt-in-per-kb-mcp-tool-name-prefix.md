---
topic: transport-auth
---

# D102 — Opt-in per-KB MCP tool-name prefix

**Decision.** Every mounted KB registers the same 20 tool names by default; a new opt-in, default-off
per-KB prefix lets an operator disambiguate them: `kbs[].tool_prefix` (explicit) or the global
`mcp.tool_prefix_mode: kb-name`/`CARTOGRAPHER_MCP_TOOL_PREFIX_MODE=kb-name` (derives the prefix from
the KB's own name for any KB without an explicit `tool_prefix`) registers that KB's tools as
`<prefix>__<tool>` instead of `<tool>` (`Server.SetToolNamePrefix`, applied once, inside
`RegisterTool`). The raw value is sanitized (lowercased, `[^a-z0-9_]+`→`_`, collapsed,
leading/trailing `_` trimmed) and validated at startup — empty or digit-leading after sanitisation,
or a resulting `<prefix>__<tool>` over 48 characters for any tool the KB registers, is a fatal config
error naming the KB and the offending name (`internal/config.ResolveToolPrefix`,
`MountKBWithPrefix`). Read/write classification and the `agent`/`full` tools profile strip the
prefix before matching (`Server.StripToolPrefix`), so scoped tokens and the profile filter are
unaffected by prefixing. `serverInfo.name` becomes `cartographer:<kb>` once 2+ KBs are mounted
(`Server.SetDisplayName`); a single-KB deployment keeps the historical bare `cartographer`.
`cartographer connect`/`sync` warn on stderr whenever the `kiro` provider is configured against 2+
MCP entries, independent of whether the server has prefixes set (`kiroFlatNamespaceWarning`).

**Rationale.** Claude Code, Codex and OpenCode namespace MCP tools per server, so a second KB's tools
never collide with the first's under those clients (verified empirically, GitHub issue #62).
Kiro CLI has one flat tool namespace across every configured server: without a distinguishing
prefix, mounting a second KB there silently drops its tools rather than erroring. Making the fix
default-off keeps the byte-identical tool surface for every client already unaffected by the
problem; making it per-KB (rather than always-on for multi-KB servers) lets an operator prefix only
the KBs that need it, e.g. to keep short names on the "primary" KB.

**Consequences.** The prefix is applied at exactly one point (`RegisterTool`), so every
conditionally-registered tool (`artifact_write`, `skill_install`, `sync_*`) is covered without a
second injection site. The 48-char budget is checked against the actual registered names after
`setupFn` runs, not computed analytically beforehand, so it naturally accounts for every tool a KB
ends up registering (including config-gated ones). The client-side warning cannot inspect whether
the server already mitigated the issue (`GET /health` doesn't expose tool prefixes), so it fires on
the precondition alone (kiro + 2+ entries) — a false positive (server already prefixed) is a
one-line stderr note, not a wrong outcome.
