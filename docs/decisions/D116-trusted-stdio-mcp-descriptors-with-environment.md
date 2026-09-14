---
topic: sync-provisioning
---

# D116 — Trusted stdio MCP descriptors with environment references

**Decision.** MCP descriptors support a strictly disjoint `stdio` transport with a command, ordered
arguments and a reference-only environment map. Commands are bare executable names or clean absolute
paths; Cartographer emits command and arguments as separate native fields, never runs a shell, and
preflights the executable before changing any provider file or lockfile. The server allow-list binds
stdio descriptors to the exact command; D114 signatures and D115 hash-bound approvals continue to
bind the entire descriptor. Claude Code, Codex, Kiro and OpenCode receive their respective native
local-MCP representation; OpenCode translates `${VAR}` to `{env:VAR}`. No environment value is
resolved, persisted in a manifest/status/log, or sent back to the server.

**Rationale.** A local MCP server has a materially stronger capability than a remote endpoint: it
causes an agent runtime to execute a binary on the client. Keeping validation, server policy,
cryptographic identity, local approval and executable preflight as independent gates preserves the
fail-closed boundary while allowing portable KB descriptors. Retaining a bare command instead of its
resolved path preserves the client operator's intended `PATH` semantics.

**Discarded alternatives.** A single `command` string split by Cartographer: it would have
reintroduced shell-quoting ambiguity between the approved descriptor and the executed process.
Resolving `${VAR}` client-side before emission: it would have written secret values into provider
configuration files that are not designed to hold them.
