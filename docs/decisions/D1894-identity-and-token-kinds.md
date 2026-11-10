---
topic: transport-auth
---

# D1894 — Tokens have a kind and an expiry; non-human kinds are never admin

**Decision.** A token carries a `kind` (`human` by default, `client`, `surveyor`)
and an optional `expires_at`. The store refuses an expired token with the same
401 as an unknown one. A `surveyor` or `client` token must expire, must declare
scopes or roles (it is never admin, so it always names its KBs), and a
`surveyor` token can never change artifacts whatever its rules say.

**Why.** The governance core of 0.29 needs the machine callers told apart from
people and bounded in time. The first formulation was "non-human tokens are
read-only", but both kinds write legitimately: a Surveyor commits content and a
`client` token is what an agent writes through, so read-only would break both.
The rule that holds is the minimal one: never admin, KB-scoped, expiring, and
for the Surveyor no artifact changes (the tool allowlist stays its own, D1257, so
the server does not duplicate the deny set). The cost: a Surveyor's content
writes are still whatever its `rw` rules grant.

**Alternatives rejected.** Read-only non-human tokens: breaks the Surveyor and
every agent client. A wildcard KB syntax for "all KBs" on a machine token: a
machine token naming its KBs is the point, so no wildcard is added. A distinct
401 for an expired token: it would tell a prober which tokens once existed.
A past `expires_at` accepted at load: a typo would silently disable a token, so
it is a startup error.

**Consequences.** A read-only token (a dashboard, a public API) is a `client`
token with `r` scopes: read-only stays the scope's decision. `Principal.Kind` is
what the component routes read to enforce the kind. The environment and flag
token forms cannot carry a kind and stay `human`. Later changes of the same
series extend this decision with reload, the secrets restriction (which also
amends D261) and the trusted-proxy identity.
