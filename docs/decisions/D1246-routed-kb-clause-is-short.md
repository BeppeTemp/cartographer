---
topic: sync-provisioning
---

# D1246 — The routed `kb` clause of the routing sentence is short

**Decision.** On a routed connection each KB's routing sentence ends with: ` Its tools are not prefixed:
where a tool takes a `kb` argument, pass `kb: "<name>"`; it is never inferred.` (105 bytes for `kb-a`,
183 before). The non-routed sentence is unchanged.

**Why.** The clause is paid once per KB on every model call of every session. The routed block of nine
KBs measured 8671 bytes, over D1182's 8192-byte cap; it now measures 7881 (3497 for one KB, 548 bytes
per added KB, about 2k tokens at nine). The sentence still carries the KB name, the `"cartographer"`
server, that tools are unprefixed (D288), the exact `kb` value, that it applies only where a tool takes
`kb` (so a single-KB connection, which has no such argument, is covered, D288) and that the KB is never
inferred (the alternative D288 rejected). It drops the explanation "that connection serves several KBs
through one set of tools", which the `kb` argument itself implies.

**Alternatives rejected.** Moving the clause to the shared section once per block: that section is
client-owned text without the routed bit, and changing it changes `PathsSectionHash` semantics. Dropping
"not prefixed": agents migrated from the prefixed names of the D288 era look for `kb_a__` tools.
Loosening the budget: refused by the maintainer.

**Consequences.** The `ContentHash` of every routed `instructions` artifact changes once, so every client
rewrites its block a single time on upgrade. This amends D1182's "the routing sentence ... is kept
verbatim"; the 12/12 procedure measurement of D1182 was taken with the old wording and is not re-run.
`TestRoutingSentence_RoutedClauseBudget` pins the clause at 115 bytes per KB at most, and the
fixed-cost budgets of `TestFixedCallCostBudget` are tightened to the new measurement.
