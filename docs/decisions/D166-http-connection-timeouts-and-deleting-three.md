---
topic: transport-auth
---

# D166 — HTTP connection timeouts, and deleting three unreachable entry points

**Status: implemented (2026-09-05).** Amends [D118](D118-fine-grained-rbac-and-permission-aware-retrieval.md).

**Context.** Two unrelated findings from an audit of the transport, grouped because neither justifies
a decision of its own and both are about the HTTP boundary being narrower than it looked.

1. **The server had no timeouts at all.** `serveHTTP` built `&http.Server{Addr, Handler}`, whose zero
   value leaves `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout` and `IdleTimeout` unset — meaning
   no deadline of any kind. A client that opens a connection and never finishes sending its request
   headers holds a goroutine and a file descriptor until the process exits. That is the classic
   Slowloris exhaustion, and it needs no authentication: the handshake never gets far enough to
   present a token.
2. **A dead pair of functions composed into a privilege escalation.** `auth.ScopesFromToken` was a
   seam for future OAuth JWT support that unconditionally returned `nil`. `auth.ContextWithScopes`
   turned an empty scope list into `Policy{Admin: true}` — reasonable in isolation, since "no scopes
   configured" has always meant full access for a static token. Composed in the shape their own
   comments invited (`ContextWithScopes(ctx, ScopesFromToken(tok))`) they authorise **any** token,
   including an invalid one, as an admin. Neither had a caller in production or in tests.

**Decision.**

- **Three timeouts, not four.** `ReadHeaderTimeout: 15s` bounds the handshake, `ReadTimeout: 60s` the
  slow-body variant, `IdleTimeout: 120s` a parked keep-alive connection. There is deliberately **no**
  `WriteTimeout`: it bounds the whole handler, so it would also cap a legitimately slow tool call — a
  full reindex, a git sync against a remote — and those are already bounded per operation, where the
  budget can be set from what the operation actually does. A write deadline here would convert a slow
  success into a truncated response, which is a worse failure than the one it prevents.
- **Both scope functions are deleted, as a pair.** `ScopesFromToken` alone is inert; `ContextWithScopes`
  alone is defensible. It is the pair that is dangerous, so removing one and keeping the other would
  leave the next caller to rebuild the same composition.
- **`Server.ListenAndServe` and `sops.DecryptAll` are deleted too.** [D132](D132-audit-authorization-denials-serve-rfc-9728-metadata.md) kept the first
  "for direct single-KB embedding and their own tests", and [D47](D47-per-kb-sops-agekeyenv-env-wins-flat-service-get.md)
  named the second in the API it was extending; neither has ever had a caller in production or in a
  test. The first was hardened with the three timeouts above before being reconsidered — which is
  what made the argument concrete: an entry point nothing reaches still has to be kept correct on
  every future change to the thing it wraps, and it had already drifted once by silently lacking the
  hardening the real listener had.

**Rationale.** Enforcement has lived in `TokenStore.ScopesOf` and the D118 authorizer since D118; the
JWT seam described a design that was never built and could not be built this way — scopes extracted
from a token are only trustworthy once the token's signature is verified, and a function taking a
bare `string` has nothing to verify against. Keeping a stub whose failure mode is "grants admin"
against a future that would not use it is a bad trade at any discount rate. `ScopesFromContext`
survives: it reads the principal that `ContextWithPrincipal` actually stores, and the middleware test
covers it.

**Consequences.** A stalled or idle connection is now reaped instead of pinned for the process
lifetime. Long-running tool calls are unaffected, since no write deadline was added — an operator who
needs one should bound the operation, not the connection. `auth`, `mcpserver` and `sops` lose four
exported functions in total, none of which had a caller; with them gone, `staticcheck` and
`deadcode` both report nothing across the module. Re-embedding a single KB over HTTP now means
building an `http.Server` at the call site, which is a handful of lines and makes the timeouts
visible to whoever is embedding it.
