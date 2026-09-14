---
topic: transport-auth
---

# D132 — Audit authorization denials; serve RFC 9728 metadata; drop the unwired HTTP handler layer

**Decision.** Three corrections, found while implementing D128 (issue #122, outside its scope).
(1) `handleToolsCall` (`internal/mcpserver/server.go`) returned as soon as `s.authorize` failed,
before `beginAuditCall` ever ran — a denial produced no audit event at all, even though D119 already
documents the attempt+completion pair with `outcome=unauthorized` as part of the contract. The denial
is now audited at the point the decision actually happens: on the `authorize` failure path,
`handleToolsCall` calls `Server.auditDenied`, which now takes the principal directly (already resolved
from the request context per D118) instead of an `*http.Request` — the dependency existed only to
re-derive the principal, redundantly, from the bearer token. (2) `docs/transport-auth.md` already
claimed the server publishes RFC 9728 Protected Resource Metadata, and `auth.go`'s `isPublicPath`
already exempted `/.well-known/oauth-protected-resource` from authentication, but nothing served it —
`auth.ProtectedResourceMetadata` had no caller outside its own test. The route is now registered in
`MultiKBServer.Handler()`, the handler `cmd/cartographer/serve.go` actually wraps in
`OriginGuard(store.Middleware(...))`; `resource` and `authorization_servers` both name this server's
own base URL, reconstructed per-request from `Host` and `X-Forwarded-Proto`, since there is no
separate OAuth authorization server to plumb through config (this server validates its own static
tokens). (3) With both fixed, `mcpAccessGuard` and the exported `FullHTTPHandler`, `WellKnownHandler`,
`ListenAndServeWithHandler` and `ListenAndServeHandler` (`internal/mcpserver/httpserver.go`) had no
caller in production or in tests — `auditDenied` was `mcpAccessGuard`'s only caller, and
`cmd/cartographer/serve.go` has always built its handler directly from `MultiKBServer.Handler()`,
never through any of the five. All five are deleted; the handful of tests that only exercised
`FullHTTPHandler` as a third member of a "same behavior across handler constructors" table lost that
one entry, not the test.

**Rationale.** An audit log exists precisely to show what was refused, and `mcpAccessGuard`'s
per-KB r/rw body-peek (D45) and `service_get`/`resolve_secrets` override (D47) had already been
re-implemented in the authorizer (`internal/mcpserver/policy.go`, D118) — nothing was unenforced, but
three comments elsewhere still pointed at the guard as the place access is decided
(`audit.go`, `tools_skill.go`), so a reader looking for the enforcement point found the wrong function
first. Serving the RFC 9728 route was the smaller of the two options available (serve it, or retract
the docs claim and the auth exemption): the docs and the exemption already existed and were correct in
intent, only the handler was missing. Deriving `resource`/`authorization_servers` from the request
rather than adding a config field keeps the change to the size the docs already promised, and is
honest about what this deployment actually is — its own token validator, not a client of a separate
authorization server.

**Consequences.** A denied `tools/call` now appears in the audit log exactly like every other call,
closing the gap between D119's documented contract and what the code did. The RFC 9728 endpoint is
self-describing rather than operator-configured: an operator behind a reverse proxy that does not set
`X-Forwarded-Proto` gets an `http://` `resource` even when the public entry point is HTTPS — no worse
than the previous 404, but not authoritative either; a future need for a precise external issuer
would need real config, not this inference. Deleting the dead handler layer removes the only place
`FullHTTPHandler`'s CORS behavior was exercised at all outside its own tests — CORS for the production
path continues to live in `OriginGuard`, unaffected by this change. `Server.HTTPHandler` (single-KB,
no `.well-known` route) and the plain `Server.ListenAndServe` are unaffected: neither is on the
production path (`serveHTTP` always mounts through `MultiKBServer`, even for one KB) but both remain
for direct single-KB embedding and their own tests, which this issue did not ask to remove.
*(Superseded in part by [D166](D166-http-connection-timeouts-and-deleting-three.md): `Server.ListenAndServe` was deleted. The "and their own
tests" half was already untrue when written — nothing called it, in production or in tests — so the
embedding case it was kept for had no exercised path and no user.)*
