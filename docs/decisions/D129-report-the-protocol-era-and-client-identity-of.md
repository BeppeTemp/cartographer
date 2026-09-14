---
topic: transport-auth
---

# D129 — Report the protocol era and client identity of connected clients

**Decision.** Each `*Server` keeps a bounded in-memory tally of the clients that reach it, keyed by
client name, client version, protocol version and era (`internal/mcpserver/clients.go`). Identity is
read from `_meta.io.modelcontextprotocol/clientInfo` in the `2026-07-28` era and from
`initialize`'s `params.clientInfo` in the handshake era — one shared `parseClientInfo`, called from
`resolveEra` and `handleInitialize`. A handshake-era request that is not `initialize` carries no
identity and is counted under the literal client name `unknown`. Recording happens in
`dispatchMethod` after the metadata authorization check, so a denied caller records nothing, and for
`initialize` after the handler has parsed `clientInfo` and settled the negotiated version. The tally
is capped at 64 distinct keys with each identity field truncated to 64 bytes (rune-safe); beyond the
cap a single `overflow` counter increments and existing keys keep counting. It is exposed by
`GET /clients` on all three handler constructors (`HTTPHandler`, `FullHTTPHandler`, the multi-KB
`Handler`), as one flat array across mounted KBs sorted by (kb, client name, version), 405 on any
method other than GET, `{"clients":[]}` and 200 on a fresh server. `/clients` is not added to
`isPublicPath`, so it inherits the bearer requirement.

**Rationale.** D128 kept the handshake era alive precisely because it is not established which
revision the real clients speak, and D130 cannot be scheduled on a guess: retiring an era needs
evidence that every client of a deployment has moved. That evidence was unobtainable — `initialize`
read `protocolVersion` into a local variable and dropped `clientInfo` entirely, and the audit log
covers only `tools/call`, so the one signal needed was structurally outside it. The audit log was
rejected as the home for this anyway: it is a signed hash chain, not the place for a soft
operational counter. The endpoint is separate from `/health` because `/health` is deliberately
exempt from auth for k8s probes, and client names and versions are deployment topology; extending it
would have published them unauthenticated. No MCP tool was added: this is operator-facing, an agent
has no use for the client roster of its own server, and every tool costs context in every KB's
`tools/list`.

**Consequences.** The question "which clients talk to this server, on which protocol version" is now
answerable on any deployment, which is what unblocks D130 — and it recurs on every future revision,
not just this one. The data is process-local and lost on restart, deliberately: durable history is
the audit log's job. Identity stays self-reported and unverified, and must never become an
authorization input — stated here so nothing is later built on it. D128's "no session state"
invariant holds: the tally is aggregate observability keyed by client identity, never by connection;
it mints no identifier and is never echoed to a client, so two requests from the same client remain
indistinguishable to the protocol. Recording is a side effect on the request path with no failure
path — hitting the cap is invisible to the caller. A hostile client can burn the 64 key slots, which
degrades the roster to an overflow count but cannot grow memory; an operator who sees `overflow`
climb should read it as noise, not as topology.
