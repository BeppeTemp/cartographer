---
topic: deployment-release
---

# D176 — The multi-KB readiness path gates on the audit sink too

**Decision.** `MultiKBServer`'s `/health` and `/ready` fold every mounted KB's
audit state into their verdict, name the degraded KBs, and expose each KB's
audit state under `kbs[].audit`. The rule itself lives in one place,
`auditGate`, shared with the single-KB handlers.

**Rationale.**

- **The audit-aware readiness code was unreachable on the HTTP path.** D119 made
  readiness gate on a required-mode audit sink so an operator, or a
  `readinessProbe`, notices before the next required-mode call is rejected. Only
  `Server.handleHealth`/`handleReady` implemented it. `MultiKBServer.Handler`
  answered `ready` from the mount count alone — and `serveHTTP` builds a
  `MultiKBServer` unconditionally, with the KB count only deciding the
  advertised server name. Every HTTP deployment, single-KB ones included, was
  therefore served by the handler that ignored the gate: a server that would
  refuse every write reported itself ready and kept receiving traffic.
- **One degraded KB makes the process not ready.** A probe must return one
  answer; the conservative one is the only safe choice, and a partially usable
  process is not a state a `readinessProbe` can express.
- **The degraded KBs are named.** Collapsing several KBs into one boolean tells
  an operator that something is wrong and nothing about where to look, which is
  the failure mode this endpoint exists to prevent.
- **`/health` stays liveness.** It keeps answering 200 with `status:"ok"` even
  when not ready: `auth.isPublicPath` exempts it, probes depend on that shape,
  and a liveness probe must never restart a process over a sink problem.
- **One shared fold.** Two implementations of one readiness rule is exactly how
  these two drifted apart; `auditGate` is small enough that sharing it costs
  nothing and asserting on it is cheap.

**Consequences.** A deployment whose required-mode audit sink is unhealthy starts
failing its readiness probe instead of silently rejecting writes — the intent,
and a change worth calling out in the release notes. Deployments with no sink, or
one in `best_effort` mode, are unaffected.
