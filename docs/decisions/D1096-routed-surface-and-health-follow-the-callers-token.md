---
topic: transport-auth
---

# D1096 — The routed surface and /health follow the caller's token

**Decision.** A routed connection serves the *effective set*: the KBs named by
`?kbs=` (after alias resolution) or every mounted KB, intersected with the KBs
the principal can read. The `kb` enum, the one-KB rule (D288) and the tool union
(D187) are computed on it. A principal that can write none of the set gets a
read-only *token view* (no write tool listed; a write call still reaches the
target KB, which answers `read_only_token`; no `initialize` instructions), and
the bridge's `?access=read` view (D1018) wins when both apply. A principal that
reads no mounted KB gets a *denied view*: `forbidden: no_kb_access` from the
metadata gate with a hint naming no KB, `tool not found` on a call, never an
HTTP 403 (D118). A `?kbs=` name the principal cannot read is `unknown`, exactly
like an unmounted one, and an alias never resolves to an unreadable target. With
auth on, `GET /health` gives an anonymous caller only `status`, `ready`,
`version`, `started_at`, `mount_mode`, `routed_path`; a valid token adds `kbs`
filtered to what it reads (always a list, never `null`) and `latest_version`.
Auth off is unchanged. To make that possible the middleware attaches, on a
public path with auth on, the principal of a valid bearer and no principal
otherwise (never the local admin, never a 401).

**Why.** A host that hands the model only tools got a menu of tools it could
never use and the names of KBs it has no business knowing, and an unauthenticated
probe received every KB name, capability and the audit sink state. Views stay
cached by (effective set, mode); principals are static configuration, so the
count is bounded and a cached `Server` never carries anything per-request.

**Alternatives rejected.** A 401 on `/health` for an invalid token: a probe or a
client with a rotated token must still get its liveness answer. Filtering
`tools/list` per request instead of per cached view: the connection's guide
(#1097) is per view. A new HTTP status for the denied view: D118.

**Consequences.** Amends D187 ("authorized as can reach at least one routed KB",
"a principal scoped to one KB lists the union"); keeps D118, D288, D756, D772,
D1018, D343. A tokenless `/health` no longer lists KBs, so readiness waits key on
`started_at`; `kb list`'s MOUNTED column needs a token or an auth-off server. A
provisioned client bound to a KB its token cannot read now fails at connection
with the D756 400. Any cache in front of `/health` must key on `Authorization`.
