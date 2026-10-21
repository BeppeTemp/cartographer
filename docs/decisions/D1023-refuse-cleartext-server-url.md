---
topic: control-plane
---

# D1023 — The client refuses a cleartext, non-loopback server URL

**Decision.** The client transport refuses every request to a non-loopback `http://`
URL unless the operator opted in with `allow_insecure_http: true` (set by
`connect --allow-insecure-http`) or `CARTOGRAPHER_ALLOW_INSECURE_HTTP=1`. Loopback
is decided by one predicate, `client.IsLoopbackHost` (`net.ParseIP(...).IsLoopback()`
plus `localhost`).

**Why.** Over cleartext the bearer token is exposed and nothing authenticates the
responses (instructions, manifests); only a `signing_keys` pin protects what gets
installed. A warning is never read in a hook-driven `sync`, and the exposure repeats
at every sync, so existing users are refused with an error naming the two fixes. It
costs a behaviour break for whoever runs a plain-http remote (LAN host, proxy
without TLS).

**Alternatives rejected.**
- A check per command: call sites are many and a new one would silently bypass it; the invariant lives in the transport.
- A warning only: not read in unattended runs.
- A flag with no persisted key: it would not survive the next unattended `sync`.
- Auto-upgrading `http` to `https` or certificate pinning: guessing a scheme can send the token to the wrong place.
- Keeping the literal `localhost`/`127.0.0.1`/`::1` lists: they refuse `127.0.0.2` and IPv4-mapped loopback.

**Consequences.** Any new code that builds a client request must go through
`client.MCPClient` (or `CheckServerURL`) and chain `WithInsecureHTTP(cfg.InsecureHTTPAllowed())`
at construction. Breaking for non-loopback `http://` users: shipped as `feat(client)!`.
