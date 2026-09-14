---
topic: control-plane
---

# D14 — Lint: deterministic checks only in the Core

Only `broken_link`, `stale_claim`, `orphan`. Reasoning checks (cross-model deep lint) deferred to the Server profile. `Now` for stale_claim is injectable for testability.
