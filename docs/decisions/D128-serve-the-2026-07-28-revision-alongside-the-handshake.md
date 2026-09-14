---
topic: transport-auth
---

# D128 — Serve the 2026-07-28 revision alongside the handshake era

*(Superseded in part by [D168](D168-the-mcp-wire-format-comes-from-the-official-sdk.md): the decision to serve both eras stands, but it is
the official SDK that implements it — the era resolution, envelope and header rules
described below are no longer Cartographer code.)*

**Decision.** The server answers both protocol generations at once and decides which one applies per
request: a request is `2026-07-28`-era iff `params._meta.io.modelcontextprotocol/protocolVersion` is
present or the `MCP-Protocol-Version` header is (`Request.resolveEra`, `internal/mcpserver/protocol.go`),
everything else is the handshake era. The era's result envelope (`resultType: "complete"` plus
`_meta.io.modelcontextprotocol/serverInfo`) is applied in exactly one place, on the way out of
`dispatch`, so no handler knows about eras; `tools/list` additionally carries `ttlMs` and
`cacheScope: "private"` in the new era. `server/discover` is implemented and answers in both eras;
`initialize` and `ping` stay routed for the handshake era. Over HTTP the three mirror headers are
validated against the body (`-32020`, HTTP 400), an unsupported version is `-32022` + 400, an unknown
method is 404 in the new era and 200 in the handshake era, and `GET`/`DELETE` on the MCP endpoint are
405. `Origin` is validated on the MCP endpoint against an operator allow-list that defaults to
same-origin, before authentication runs, and the CORS allow-origin header echoes the accepted origin
instead of `*`. The `resources` capability, which no method ever backed, is no longer advertised.
The CLI client's reachability probe moves from `ping` to `server/discover` and now surfaces the
JSON-RPC error carried by a 400 or 404 instead of the bare status.

**Rationale.** What `2026-07-28` removes — the handshake, sessions, SSE resumability — is what this
server never had, so the architecture already matched the new model and only the protocol surface
lagged. Flipping the version outright was not defensible: the actual consumers are Claude Code,
Codex, Kiro and OpenCode, and it is not established that any of them speaks the revision yet, so a
flip would have broken every live KB client to satisfy a spec no client had asked for. Serving both
is cheap precisely because the server is stateless — the era is a property of a request, not of a
connection, so coexistence costs no bookkeeping. The Go SDK was rejected: adopting it would mean
rewriting `dispatch`, the policy gate, the audit wrapping and the tool-prefix machinery, all built
around the hand-rolled `Request`/`Response`, to close a conformance delta of a few hundred lines —
and it would add this project's first protocol dependency. The mirror headers are validated rather
than trusted because they are client-controlled and duplicate what the body already says; letting an
authorization decision read them would create exactly the split source of truth the spec mandates
the validation to close. `Origin` validation has been a MUST since `2025-03-26` and was simply
missing; a wildcard allow-origin alongside it is what turns a rebinding attack into a readable
response.

**Consequences.** Existing clients are unaffected: a handshake-era request produces byte-identical
responses, which is asserted directly (`TestHandshakeEra_ResponsesUnchanged`) and indirectly by the
e2e suite passing unedited. Operators who expose the server to browsers must now set
`mcp.allowed_origins`, or accept the same-origin default — the one behaviour change that can turn a
working setup into a 403, which is why `"*"` exists as an explicit opt-out. The same-origin default
is not by itself a rebinding defence, since a rebound name matches both `Origin` and `Host`; the
allow-list is. `resources` disappearing from the capability map costs nothing observable, since a
client acting on it got `-32601` anyway. `skills.listChanged` stays, because `notifyWrap` and
`artifactNotifyWrap` do emit that notification — an earlier reading that called it unbacked was
wrong. The CLI client deliberately stays handshake-era, `_meta`-free: the server serves both, and
moving the client only pays off once the handshake era is retired, which is deferred to D130 and
gated on the per-request era roster of D129.
