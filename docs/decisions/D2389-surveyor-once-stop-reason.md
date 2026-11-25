---
topic: deployment-release
---

# D2389 — A task that refuses for want of budget ends the run as budget

**Decision.** A task kind reports a refusal for want of budget in `Result.Budget`, and the runner then ends the run `stopped=budget`, as it does for a grant at or below zero. Every run, `--once` included, logs `surveyor: run ended stopped=<reason> tasks=<n>` on exit.

**Why.** The ingest kind refuses a unit when the grant is positive but below its floor, and leaves the unit claimable. The runner's own gate only looks at a grant of zero or less, so the refused unit was marked tried, `Next` kept offering it, and the run ended `stopped=done` with work left. An external scheduler of `--once` saw `done` and could not tell a drained queue from a refused one.

**Alternatives rejected.** Making `Next` skip units already tried: a contract change across every task kind, for a state the refusing kind knows best. Having the runner learn the floor: it would duplicate a rule that belongs to the kind. A `--drain` loop around `tick`: the loop already exists in `work`; the defect was the stop reason.

**Consequences.** A kind that refuses to start for want of budget must set `Result.Budget` and leave nothing claimed. The `stopped=` values are unchanged, so the closing entry's format, which the budget reader parses, is too.
