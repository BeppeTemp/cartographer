---
topic: control-plane
---

# D1987 — A secret reference can be resolved by a hook, at the point of use

**Decision.** A `secret_refs` item `NAME=hook:<plugin>/<name>#<key>` is resolved by that
`secret_resolver` hook (D1957) when `secret_resolve` or `service_get` resolves the concept,
outside the KB lock, through the same function as the sops references; both forms mix in one
concept and a `NAME` is declared once across them. The hook is sent the KB, the concept, the
name, the opaque key and the principal, and answers a `value` with an optional `ttl_seconds`.
The core's checks run first and are unchanged: `rw`, `secrets: none` (D1894), redaction unless
`reveal` (D158, D261).

**Why.** An external vault must be usable without copying its secrets into a KB, and without the
server learning a vault's protocol. The resolver is a hook rather than part of `internal/sops`:
that package is the hermetic `sops` child (D260) and must not learn HTTP, and a hook never sees
the age key. A resolver is `fail_closed` by construction (no value is no resolution) and `mode: dry_run`
is refused for it: a dry run hands back no value (D1957), and silently enforcing it instead would
hand out live values to an operator who asked to observe.

**Alternatives rejected.** Resolving at write time and storing the value: it puts the secret in
git. Caching by default: a resolver that decides per caller would be bypassed; the hook opts in with
`ttl_seconds`, bounded by 300 s and the operator's `cache_ttl`. A cache keyed by hook and key only:
a resolver may answer per KB, so the KB is part of the key. Auditing the key: it can itself be
sensitive (a path in the vault); the completion event records the hook names only
(`secret_resolver`), which the handler hands to the audit through `ToolResult.AuditResources`.

**Consequences.** A hook reference names no file, so lint's `secret_ref_broken` leaves it alone; one
naming a hook the server does not configure as a resolver is a broken reference at resolution. A
cached value is reused for every caller the core lets resolve that reference, for its TTL. The
handlers reach the registry through the request context, set by `callToolChecked` only when hooks
are configured, so a server without hooks resolves exactly as before.
