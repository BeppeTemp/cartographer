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
series extend this decision with the secrets restriction (which also amends
D261) and the trusted-proxy identity.

**Secrets.** D261 left `rw` as authority over every secret the KB's age key
decrypts, because the only alternative was a separate KB. A rule now carries
`secrets: none`, a narrower allow like `artifacts: r` and not a deny: the
secret tools, `service_get` with `resolve_secrets` and the concept writers whose
arguments name `secret_refs` or `secrets_source` answer `secrets_restricted`
unless some other, unselected `rw` rule of the token lacks the key. The
concept scan is conservative (a false positive denies) because the
frontmatter reaches the tools in several shapes; reading the reference names
stays allowed. The concept writers are denied when the token has at least one
write rule on the KB carrying `secrets: none` and no unselected write rule
without it, so a scoped rule without the key does not undo a whole-KB
restriction; a token with no `secrets: none` rule is unrestricted as before.
`concept_transfer` carries frontmatter verbatim, so it is checked against the
target KB. The artifact tools need no check: the secret kind is not an
artifact path they accept. The `surveyor` kind forces the restriction, and the
Atlas hides the secret files of a KB whose unselected rules all carry it. D261
is amended, not reversed: no key means today's behaviour.

**Reload.** Tokens and roles are applied to a running server: the store holds an
immutable snapshot behind an atomic pointer and `Replace` swaps it whole, on
`SIGHUP` and on a poll of the config file (`auth.reload_interval`, default 30s,
0 off). A reload that fails validation, turns auth off, names another auth mode
than the running one, or would leave no usable token, keeps the old set (the
D179 fail-closed rule); a refused content is retried by the poll until the file
changes, so a half-saved edit that is completed applies. A token that expired
after startup is not a validation error on reload: it is left out of the new
set with a log line, because it already fails at lookup and the edit that
removes a leaked token must not be blocked by one that merely ran out (startup
still refuses a past `expires_at`). The default is on because a file edit that
waits for a restart nobody remembers is how a revoked token stays valid.

**Proxy identity.** Behind an SSO proxy the person is the principal. The proxy
authenticates with a bearer token declared `proxy: true` (never admin, kind
human or client); the identity header is honoured only when the TCP peer is in
`auth.proxy_identity.trusted_addresses` (CIDRs, `/0` refused, no implicit
loopback, `X-Forwarded-For` never consulted) AND the bearer is a proxy token.
The groups map to roles through `group_roles` and the result is clamped by the
proxy token's own policy, restrictions included, so the proxy token is the
ceiling of every person behind it and a read-only SSO bearer stays read-only.
Anywhere else the header is ignored, and an absent header leaves the request
the proxy token's own, so a setup with one injected bearer keeps working
unchanged. The block lives in the token store snapshot and reloads with tokens
and roles. The ceiling is applied per KB with whole-KB rules only: a proxy token whose
roles select maps, journals or types is refused at load (a selector-scoped
ceiling would not bound a whole-KB group role), `Clamp` also drops a mapped
permission on a KB where the token has no whole-KB rule of the needed mode,
and the person gets the secrets restriction the token's visibility and
authority imply. Token ids starting with `user:` are reserved for people.
Direct OIDC stays a plugin filling `auth.IdentityResolver`.

**Attribution.** An audit record carries `principal_kind` (the kind of the token
behind the principal) and `via` (the proxy token's id when a trusted proxy named
the person). They are trailing `omitempty` fields of the canonical entry, hashed
only when present like `result_bytes` (D301), so every record written before
verifies unchanged; the canonical struct only ever gains fields of this shape.
