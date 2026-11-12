---
topic: control-plane
---

# D1971 — An identity hook supplies names, never permissions

**Decision.** An `identity` hook is asked in `ProxyConfig.resolve`, after the trust
checks and the header reader, only for a request on a `proxy: true` token from a
trusted peer that carries a header identity. Its `{user, groups}` replaces the header
identity; the group map and the clamp by the proxy token then run unchanged. Under
`fail_closed` a failure is a `503` with `Retry-After`, under `fail_open` the header
identity stays. The answer is cached for `cache_ttl` in a table of 4096 entries.

**Why.** This is the one hook on the path of every request, so it must not widen what
the core grants (names, not roles, and the ceiling is the proxy token) and must not
cost a call per request (cache) or hang the server on a dead hook (the registry's
breaker). Calling it only when the header names someone keeps the cache key honest: the
header identity is part of it, together with the listed header values, so one user's
answer is never served to another.

**Alternatives rejected.** A hook for requests without a header identity: its cache key
would be empty or session-only, and a shared entry would cross users. Hook-supplied
roles: a plugin would grant permissions. A cache keyed on the user header alone: it
ignores the other headers the hook is shown.

**Consequences.** Direct OIDC stays out of the core: a plugin behind the proxy
validates the session and answers names. A second identity hook, or one without
`auth.proxy_identity`, refuses the start.
