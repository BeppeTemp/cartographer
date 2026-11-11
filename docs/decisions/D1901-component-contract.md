---
topic: control-plane
---

# D1901 — One versioned route family for what components report

**Decision.** What the Surveyor and Compass report to Meridian goes through one
route family, `/api/component/v1/{heartbeat,settings,usage}`, named in the leaf
package `internal/componentproto`. The path version is the envelope (auth,
content type, error shape, body caps) and changes essentially never; an integer
`protocol` (1 in 0.29) is the field-level contract and is negotiated. Meridian
serves a sender at N-1, N and N+1 and refuses everything else with `426`
`protocol_unsupported` carrying its own `protocol` and `min_protocol`; a sender
at N+1 is served as N and stored with `protocol_skew` `ahead` (`behind` at N-1).
The newer sender's obligation is to speak the server's N, which it reads from the
heartbeat answer or from `/health` (`component_protocol`, `component_min_protocol`).
The Surveyor refuses an unsupported server at start-up and never exits on a `426`
afterwards. `/health` stays liveness and never reports a component.

**Why.** Four unrelated, unversioned metadata routes carried no compatibility
signal: "404 means an older server" was the whole story, while a Helm release can
ship the Surveyor and Meridian at different times, so skew is a normal state. The
rule is symmetric so a rolling upgrade works in either order.

**Alternatives rejected.** A version in the path only (`v2` for every change):
forces a second implementation for an added field. A header or a negotiated
`Accept`: invisible in logs and awkward for the usage route, whose body is an
array (its `protocol` is a query parameter instead). Failing the Surveyor on `426`:
its budget is enforced locally and Meridian may be upgraded under it, so it logs
once per distinct window and keeps beating.

**Consequences.** The released routes `/api/usage` and `/api/client-settings`
(0.28 clients in the field) stay served for the whole 0.29 series as frozen
aliases of the same handlers at protocol 0, with no negotiation; they can be
removed once no supported client predates the family. A newer Compass facing a 0.28 server (404 on the new path) retries once on the 0.28 route with the 0.28 body and remembers that for the process, so no report is lost to skew; the Surveyor has no such fallback. `/health` gains `component_protocol` and `component_min_protocol` for every caller, anonymous included: an amendment of the anonymous key set of D1096 (two integers, no secret and no inventory). The Surveyor's
`/api/surveyors`, `/api/surveyor-settings` and `GET .../workers` were never
released and are removed with no alias; the dead doctor-schedule client methods
are deleted. A new component route goes through `componentproto` and the route
table test. The settings route selects the report by the body's `component`
(`surveyor` or `client`). Later changes of the series add the registry of expected
components (the `silent` and `starting` health states, a grace window after the
server starts), the per-KB `components` and `maintenance` objects and the kind
check on these routes (`403` `wrong_token_kind`); their states and codes are
already named in `componentproto`. Everything stays in memory like the heartbeats.

**The registry (`components:`).** The expectation lives in the server config, not
in the Surveyors: the server cannot otherwise tell a dead Surveyor from a
deployment without one, because a silent heartbeat is forgotten on the next
sweep. The key is a `kind` (only `surveyor` in 0.29) so later kinds need no new
key; `name` is the perimeter; an entry naming a KB the server did not mount is a loud warning at start and
is kept (a KB that failed to mount must not become an outage of the others). The states are computed
on read from the live heartbeats and a `last seen` kept only for names in the
registry (bounded by it). A server that has just started gives an expected
component a grace window of three heartbeat intervals (`starting`), so a pod that
boots with the server is not an alarm. `/health` and `/ready` stay liveness
(D84, D119): a silent Surveyor must never restart the server, so the state is
read through the authenticated components route and logged once per transition.

**Health read and the kind check.** The Atlas reads `GET /api/ui/v1/kbs/{kb}/components`
(registry rows plus live undeclared Surveyors, replicas named by the id they declared,
never by principal) and a `maintenance.repair` object inside the maintenance summary:
`owner`/`state` come from `repairOwners`, the decision that pauses the server's timer, so the
two cannot disagree; `fallback` is an expected Surveyor last seen declaring `repair` that is
now silent. The transition is recorded only when someone reads it. The versioned routes
refuse a token of the wrong machine kind with `403` `wrong_token_kind`; the aliases, the
auth-off principal and `human` are not refused, because a token with no declared kind loads
as `human` and the two cannot be told apart: enforcement bites only where an operator
declared a machine kind. A token declared `surveyor` is excluded from the idle signal by
kind. Known limit: a registry entry is not bound to a principal, so a writer of a KB can
impersonate the expected perimeter on it.
