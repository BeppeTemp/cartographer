---
topic: control-plane
---

# D2042 — an MCP approval keeps the approved text

**Decision.** The client-side approval of an MCP descriptor stores the approved descriptor text (`content`, optional) next to its hash, so a re-approval shows a diff of the changed lines from the last approved version, and `approve --sync` (and `revoke --sync`, `--pending --sync`) runs a sync right after a successful write.

**Why.** The approval kept only a hash, so "what changed since I approved it" had no bytes to compare. A descriptor holds `${VAR}` references and never a literal value (`validateEnvRefs`), so storing it puts no secret in the machine file. Approve then sync was always two commands; the flag is one.

**Alternatives rejected.** Refetching the previous version from the KB history: the client cannot tell which commit it approved, and the KB may be unreachable. Storing a hash of each field: no readable diff.

**Consequences.** `MCPApproval` gains an optional `content` field. An older client rewrites the machine file from its own struct and drops it, so the diff degrades to "previous approval has no stored version" until the next approval. Only MCP approvals store text. A refused, declined or failed approval never syncs.

## Sync and status say why a client got no descriptor

**Decision.** `sync` prints `[<client>] mcp <name>: not written: <reason>` (`would not write` on `--dry-run`) and `status` lists the same reason (`mcp_not_received` in `--json`, additive) for every MCP descriptor a bound client does not receive. One reason by priority: no workspace destination for the client, a pending approval, a tracked MCP file a derived binding never edits, a Codex project that is not trusted. It is one function (`mcpNotReceivedReasons`) that sync and status share, and the tracked-file text is the one `workspace status` already printed.

**Why.** A derived binding that skips a git-tracked `opencode.json` or `.codex/config.toml`, or an untrusted Codex project, left the client without the descriptor and `sync` silent, while `.mcp.json` clients got it (#1911). The projection stays as it is (D1019): the fix is to say it.

**Consequences.** Nothing is written to a client. A client that receives the descriptor but not its headers is not listed: the writer's existing `warning:` says so. `connect` prints no such line.

The later changes of this plan (the probe, the `requires` label) extend this file.
