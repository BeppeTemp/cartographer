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

## `mcp probe` diagnoses a descriptor in layers, with one exit code per layer

**Decision.** `cartographer mcp probe <kb>/<slug>` takes an approved descriptor from the same manifest `approve` reads and checks, in this order and without sending MCP bytes: the `${VAR}` references unset or empty in the probing shell (`contract.MCPEnvRefs`), then for `http` a DNS + TCP + TLS dial classified into `dns`, `refused`, `timeout`, `tls_unknown_authority`, `tls_other` (`internal/mcpprobe`). Exit codes: `0` ok, `1` the server answered but is not usable, `2` usage, not found, ambiguous or not approved, `3` unresolved variable, `4` network, `5` TLS. The first fault in that order decides the code, and the report lists every finding.

**Why.** "Connection closed" from a client hid three different faults with three different remedies: a variable unset in the shell the client was started from, a network that drops packets, a server that omits its intermediate certificate. A separate code each lets a script and a person tell them apart without reading a stack.

**Alternatives rejected.** Launching the server to see what happens: it runs a descriptor's command before the person has seen the cause. Reading the client's own environment: not possible for a GUI client, so the report says what it cannot see.

**Consequences.** The probe runs in the shell of the person, not of the client. `${secret:...}` references are the launcher's and are not variables. The dial ignores the system proxy. A descriptor that passes these checks exits `0` until the session part lands; `mcp probe` is not released between the two changes.

The later changes of this plan (the session part of the probe, the `requires` label) extend this file.

## The probe session starts a descriptor through `mcpexec.Resolve`

**Decision.** After the static layers pass, `mcp probe` calls `mcpexec.Resolve` (the resolution `mcp exec` runs: approval check against the server's copy, `${VAR}` and `${secret:...}` expansion) in its own process and talks MCP to the result with the Go SDK: a command transport for stdio, a streamable HTTP transport with the resolved headers (a redirect to another host refused) for http. It does not spawn `mcp exec` as a child.

**Why.** The probe then sees exactly the values and refusals the launcher would, without a second process to scrub, and the secret values stay in memory next to the code that redacts them. The cost is that a client's native entry (no launcher) is tested through the launcher's resolution, which is the same expansion.

**Consequences.** A probe of a descriptor with a `${secret:...}` reads the secret from the server like a client start does. Every error text goes through a scrub of the resolved values; the launched child is killed by context cancel (`WaitDelay` 2 s), without the process-group kill the Surveyor's connector adds.

