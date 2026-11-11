---
topic: control-plane
---

# D1910 — The UI API routes are a declarative table with a tier

**Decision.** Every `/api/ui/v1` read route is one entry of `apiRoutes` (`internal/mcpserver/apiroutes.go`): a pattern, a tier (`stable`, `preview` or `internal`), a summary and a handler. `serveAPI` matches the table, applies the method rule and the KB visibility check once, and serves only the tiers its mount offers; the Atlas mount offers all of them.

**Why.** A second consumer of the same handlers needs to know which routes it may serve and how stable each is; a segment `switch` cannot say that, and a route listed in prose drifts from the code. The table gives a route one definition and is the single place a tier is decided.

**Alternatives rejected.** Keeping the `switch` and a parallel list of public routes: the two drift. A router library: the server is plain `net/http` and the table needs only literal segments plus `{kb}`.

**Consequences.** A new read route is added to the table with a tier, never by a `case`. Behaviour of the Atlas mount is unchanged (status codes and bodies). A route outside the served tiers answers the same 404 as an unknown path. `components` and `maintenance/summary` are `internal`; the component report routes under `/api/component/v1` are not UI routes and stay out of the table.
