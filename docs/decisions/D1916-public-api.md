---
topic: control-plane
---

# D1916 — A public read-only API at /api/v1, opt-in, from the same route table

**Decision.** The route table of D1910 has two mounts: the Atlas's `/api/ui/v1` (every tier) and `/api/v1`, which serves the explicit mask `stable|preview` and exists only when `api.enabled` is set. It is independent of `web.enabled`, uses the MCP bearer tokens and RBAC, and answers an `internal` route with the same 404 as an unknown path. Only `/api/v1/openapi.json` is exempt from the token, because it holds no data.

**Why.** External dashboards and other Atlases need a read surface with a stability promise that the Atlas's own API cannot give. One table means a public route has the same handler, the same visibility filtering and the same non-disclosure rules as the Atlas's.

**Alternatives rejected.** Moving the Atlas onto `/api/v1`: its internal routes would be one tier away from being public. A second set of handlers: the two would drift and the filtering would be written twice. On by default: it widens the HTTP surface for every operator who did not ask. A mask of "every tier but internal": a tier added later would be public by default.

**Consequences.** The public mount carries no operator wording (no `withOperator`). A new route is added to the table with a tier; the mount never needs a `case`. Config keys `api.enabled`, `api.cors_origins`, `api.max_streams_per_token`, `api.max_streams` are server-scoped and need a restart; a bad CORS origin stops the start. The OpenAPI document, the CORS behaviour and the event stream are later changes of the same API and are recorded when they land.
