---
topic: control-plane
---

# D1305 — `kb_status.connection.hint` is optional, and `status` has a `starting` state

**Decision.** `kb_status.connection.hint` is present only when there is something to act
on: a name in `unbound`, `unknown` or `renamed`, each adding its own sentence in that order
(the operator and generic wordings of D1101 are unchanged); otherwise the key is absent.
`cartographer status` reports a server whose `/health` says it is bootstrapping as
`reachable: true`, `ready: false` with `error.code: "starting"` (state `unavailable`,
exit 2), never as a network failure.

**Why.** The old hint was always sent, and when `kbs` equalled `mounted` it described a KB
that was not there, so an agent either acted on nothing or learned to ignore the field. A
bootstrapping server answers, so "could not reach … check the configured URL" sent the
operator to fix a URL that was right.

**Alternatives rejected.** An empty-string `hint`: a consumer must still test two shapes.
Keeping the always-present hint with softer wording: it still names no fact the lists show.
Reusing `request_failed` with a different message: a script cannot tell a retry from a fault.

**Consequences.** A consumer must treat `hint` as optional and `error.code: "starting"` as
retryable, not as a failure of the configuration. A new hint sentence needs its own fact in
the same object and a place in the order above.
