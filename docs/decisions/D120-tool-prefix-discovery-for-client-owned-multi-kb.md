---
topic: transport-auth
---

# D120 — Tool-prefix discovery for client-owned multi-KB operations

**Decision.** `GET /health` now advertises each mounted KB's effective tool-name prefix
(`mcpserver.KBInfo.ToolPrefix`, populated at mount time), and every client-owned direct tool call is
qualified from that snapshot rather than from a locally recomputed prefix
(`resolveKBTargets`/`qualifyTool`/`callTool` in `cmd/cartographer/multikb.go`, used by `sync`,
`reindex` and the TUI). The discovered value is used live and never persisted. The TUI's MCP-config
badge becomes three-state — `in-sync`, `partial`, `missing` — computed against **all** expected
multi-KB entries instead of collapsing an incomplete configuration to `missing`. Remote failures
carry a typed taxonomy that separates a server that never answered from a server that answered with
a protocol or tool error, so the latter is no longer displayed as `unreachable`. This is a corrective
extension of D102: the prefix remains opt-in and default-off.

**Rationale.** D102 let an operator choose an arbitrary `tool_prefix`, but the client kept deriving
the namespace from the KB name. On any installation whose prefix was not exactly the sanitised KB
name, every client-owned call named a tool that did not exist. The symptom reached the operator as
two false diagnostics — `mcp-config missing` and `artifacts: server unreachable` — that pointed at
the network and the provider config while the server was healthy and correctly configured. Discovery
is the only sound fix: the prefix is server state, so the server must report it. Not persisting it
keeps client and server from drifting when a prefix changes. The badge and the error taxonomy are
part of the same defect: a diagnostic that misattributes a failure costs more than the failure.

**Consequences.** `/health` grows a field; the key is omitted when empty, so an older client parsing
the response is unaffected and an unprefixed deployment sees a byte-identical body. Every direct
tool call now depends on a successful `/health` first — a client that cannot reach health cannot
qualify a call, which is why an unreachable server is reported as exactly that and not as a tool
failure. The three-state badge means an operator who previously read `missing` on a partially
provisioned multi-KB setup now reads `partial`: same underlying state, but it no longer suggests
nothing was written. `server_url` in the client config is still expected to include the `/mcp` path
segment; `/health` is derived from it by stripping that segment, unchanged from before.
