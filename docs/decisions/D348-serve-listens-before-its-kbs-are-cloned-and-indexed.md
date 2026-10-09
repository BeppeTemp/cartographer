---
topic: deployment-release
---

# D348 — `serve` binds its port first and bootstraps the KBs behind a gate

**Decision.** In HTTP mode `serve` starts listening before any clone or index
work. A `BootGate` handler answers `/health` (always 200, `bootstrapping:true`
and a diagnostic `phase`) and `/ready` (503) itself and refuses every other path
with `503 kb bootstrapping, retry` + `Retry-After: 5`; when every KB is cloned,
opened and reconciled the real handler is installed in one atomic step.
Bootstrap failures stay fatal. Clones are atomic (temporary sibling directory,
then rename). Clients treat a bootstrapping `/health` as an error ("retry"),
never as an empty KB list.

**Why.** On a fresh pod with an `emptyDir` the clone plus first index build took
minutes with the port closed, so a liveness probe killed the pod and clients got
connection errors. Swapping one handler pointer is race-free;
`MultiKBServer` keeps plain maps read without a lock, so mounting KBs into a
live server would have needed a large concurrency rewrite.

**Alternatives rejected.**
- Per-KB incremental mounting: the routed mount registers the union of all KBs'
  tools and sibling roots are cross-wired, so a half-mounted server cannot be
  routed correctly.
- A new readiness endpoint or config key: `/ready` already exists; the
  operator-side fix for a slow cold start is a `startupProbe`.
- Reporting `status != "ok"` while bootstrapping: D84's rule is that a probe
  must never restart the process for a KB-mounting reason.
- Making bootstrap errors non-fatal: a wrong remote or key would leave a
  permanently "bootstrapping" server instead of a visible crash loop.

**Consequences.** `/health` gains `bootstrapping`/`phase` only while
bootstrapping (steady-state shape byte-identical). Any new per-KB wiring in
`serve` belongs in `bootstrapKBs`, any handler-chain change in the goroutine of
`serveHTTP`, and a client must never read an empty `kbs` from a bootstrapping
answer (`client.Health`, `fetchHealth`, `service.ProbeHealth` return an error).
A half-clone left by a binary older than this one is not detected.
