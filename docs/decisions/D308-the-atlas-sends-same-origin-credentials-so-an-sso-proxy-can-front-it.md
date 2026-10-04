---
topic: deployment-release
---

# D308 — The Atlas sends same-origin credentials so an SSO proxy can front it

**Decision.** The Atlas UI calls `/api/ui/v1` with `credentials: "same-origin"`,
so cookies of the page's own origin travel with every API request. The bearer
token stays in memory and is still never persisted.

**Why.** A reverse proxy that puts SSO in front of `/ui` and `/api/ui` (forward
auth, optionally injecting a read-only bearer) authenticates the browser with a
session cookie. With `credentials: "omit"` that cookie never reached the API:
the proxy answered every call with a login redirect, which `fetch` reports as a
network failure, and the Atlas showed "Server unreachable". It costs nothing
for direct use: a deployment with no cookie sends none.

**Alternatives rejected.** `include`: also sends cookies cross-origin, which the
Atlas never does. Leaving `omit` and protecting only `/ui`: the API would be
reachable without SSO. Persisting the token in `localStorage`: contradicts D227.

**Consequences.** A cookie set on the Cartographer origin is now sent to its
own API; the server ignores cookies and authenticates by bearer only.
