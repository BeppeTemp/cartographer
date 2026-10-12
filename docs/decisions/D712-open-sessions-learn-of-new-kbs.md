---
topic: control-plane
---

# D712 — Open agent sessions learn of a new KB through a stateless routed notice

**Decision.** The routed mount tells an agent which KBs its connection serves and which the server mounts: `kb_status` on `/mcp/routed` carries a `connection` object (`kbs`, `mounted`, `unbound`, `server_started_at`, fixed `hint`), and a call naming a KB outside the connection set is refused with a text that distinguishes "mounted but not bound" from "not mounted" and names the fix. `kb create` guidance adds that open agent sessions need a new session after `cartographer sync`.

**Why.** An open session keeps the `kb` enum of its first `tools/list` and the instructions `sync` wrote; HTTP has no list-changed notification and KBs mount only at process start. The server holds no per-session state, so it publishes facts and one fixed hint and the agent compares them with what it knows. The notice is a JSON field, not an extra content block, because CLI callers decode `Content[0]` only. It costs a decode and re-encode in the routed wrapper.

**Alternatives rejected.** Resolving each client's binding on the server (changes the D288 contract that the binding travels in the URL and only narrows, and the auth model; needs its own plan). Hot-mounting KBs (every session is lost on restart anyway). A notice in `initialize` instructions (an open session has already consumed them). Putting the field in `kb_status` of the per-KB endpoints (CLI and Atlas callers would see a new shape).

**Consequences.** The `connection` key exists on the routed mount only and is additive; no URL, config or schema changes meaning. A future dynamic binding must replace, not extend, this notice.
