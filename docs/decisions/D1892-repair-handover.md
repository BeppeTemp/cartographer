---
topic: architecture
---

# D1892 — a Surveyor can own the repair of its KBs: `NoModel` task kinds and the declared `tasks`

**Decision.** A task kind may be `NoModel`: its entry sets no model profile (a `model:` key is a load error), the runner builds no model client for it and applies no grant gate, and a config whose every task is `NoModel` may omit `models:`. The `repair` kind is the first: once per `repair_interval` per KB (default 24h, at least 1h, in memory) it calls `kb_repair` with `check: auto`, the same staged repair as the server's timer. The heartbeat gains an optional `tasks` list (`{kind, interval_seconds?}`, at most 16, interval clamped to 3600..691200) saying which task kinds a Surveyor runs.

**Why.** Repair is mechanical and needs no tokens; faking a model profile for it would put a meaningless budget and a model client in the config. The declaration is what a later change reads to let the server's own timer step aside for a KB a live Surveyor repairs; it is additive on both sides.

**Alternatives rejected.** A dummy profile with a zero budget: the grant gate would skip the task forever. A new route for the declaration: the heartbeat already carries what the Surveyor is. Deriving the declaration on the server from the settings report: that report is constant per process and not tied to liveness.

**Consequences.** Any new `NoModel` kind must never reach a model (`Runtime.Profile` and `Model` are zero). The server stores `tasks` and shows nothing of it yet; the handover itself and its success condition are decided in the change that uses it, which extends this file.
