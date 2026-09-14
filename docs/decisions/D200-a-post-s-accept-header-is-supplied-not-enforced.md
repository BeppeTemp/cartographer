---
topic: transport-auth
---

# D200 — A POST's `Accept` header is supplied, not enforced

**Status: implemented.** Closes #273 (problem 1).

**Context.** Since [D168](D168-the-mcp-wire-format-comes-from-the-official-sdk.md) the SDK's Streamable HTTP handler refuses with 400 any POST whose
`Accept` does not name both `application/json` and `text/event-stream`. Google Antigravity sends
`Accept: application/json` alone on its notifications (`notifications/roots/list_changed`), so its
session was dropped right after a successful `initialize`.

**Decision.** `Server.handleMCP` appends `application/json, text/event-stream` to every POST's
`Accept` before handing it to the SDK, on a clone of the request.

- **The requirement buys nothing here.** The server runs `Stateless: true, JSONResponse: true`: every
  answer is one JSON body or a 202, never an event stream. Refusing a client for not accepting a
  media type it will never receive protects nothing.
- **Appending, not parsing.** The SDK checks only that both types are present, and nothing else reads
  `Accept`, so adding both is equivalent to re-implementing its parser to add only the missing one —
  with nothing to drift when the SDK changes its rule.
- **Not a transport change.** Well-formed clients see byte-identical behaviour; GET and DELETE keep
  their 405.

**Consequences.** The server is lenient where the specification asks the client to be strict. If a
streaming response mode is ever introduced, this has to become a real negotiation, since a client
that genuinely cannot read an event stream would then receive one.
