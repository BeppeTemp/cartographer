---
topic: deployment-release
---

# D84 — Readiness signal and per-KB path routing

**Status: implemented (2026-07-23).**

**Context.** Follow-up of D73's "0 KBs, `/health` active" attached choice: `/health` always returns
`status:"ok"`, even with 0 KBs mounted, while the functional endpoint 400s (`kb parameter required`
with no `?kb=` and more than one KB, or zero). No liveness/readiness distinction — agents and
`connect` see a "healthy" server that is unusable. Separately, `/mcp/<name>` (implied by `kbs[].name`
as "the name used everywhere", D53) was probed by a real user and not implemented: bare `/mcp`
auto-routes only with exactly 1 KB, `?kb=` works, any other path 404s.

**Decision.**
- **WP1 — path routing.** `MultiKBServer.Handler` recognizes `/mcp/<name>` alongside the existing
  bare `/mcp` (single-KB auto-route) and `/mcp?kb=<name>`. Unknown name → `404 unknown kb`. If both
  the path and `?kb=` are present and name different KBs → `400 conflicting kb selection` (explicit
  over silent precedence).
- **WP2 — readiness.** `/health` (single-KB and MultiKB) gains `ready: <bool>` (single-KB: always
  `true`; MultiKB: `len(kbs) > 0`); `status` stays `"ok"` unconditionally — readiness is additive,
  it must never change liveness semantics for existing probes. New `/ready` endpoint on both
  handlers: `200 {"ready":true}` / `503 {"ready":false,"kbs":0}`.

**Rationale.** Keeping `/health` as pure liveness (never fails from a KB-mounting issue) avoids
turning a "no KBs mounted yet" state into a restart-loop on k8s if `livenessProbe` were pointed at
it; `/ready` gives `readinessProbe` a signal that actually reflects usability. Path routing closes
the gap between what D53 already documented as "the name used everywhere" and what the HTTP layer
actually accepted.
