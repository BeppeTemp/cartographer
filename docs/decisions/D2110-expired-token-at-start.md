---
topic: transport-auth
---

# D2110 — A token that expired while configured does not stop the start

**Decision.** `serve` judges `expires_at` with the zero time at start, as a reload
does: a token whose expiry is already past stays in the set, the store refuses it
at lookup, and the start logs its id and date (never the value). An unparseable
`expires_at` still refuses the start. This amends D1894, whose "a past
`expires_at` is a startup error" now holds only for `ValidateAuthAt` called with
a real instant.

**Why.** A pod that restarts after a token's expiry crash-looped with an error
that did not say to remove or renew it, while the same token on a running server
was tolerated by the reload. The typo guard D1894 wanted cost an outage for a
token that merely ran out. The cost: a mistyped past date is no longer caught at
start, only by the log line and the 401.

**Alternatives rejected.** Dropping expired tokens before `resolveAuth` at start:
an all-expired set would leave no token and, in mode `auto`, authentication off,
a fail-open. Keeping the error and naming the fix in its message: still a crash
loop. Loosening `ValidateAuthAt` itself: other callers rely on it being strict.

**Consequences.** The token must keep counting as configured so
`resolveAuth` and `RequireAuth` keep auth on; any later change that drops an
expired token at start must keep an all-expired set enforcing (401). The log never
carries a token value.
