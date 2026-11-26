---
topic: project-governance
---

# D2435 — Unqueued finished work is parked on origin

**Decision.** A maintainer's change that is not queued when its session ends is pushed to `wip/<slug>` on origin with one `needs-maintainer` issue naming what blocks it. `make unlanded`, `make issues` (class `parked`) and `make status` list it, `make ready` deletes the branch once `ready/<slug>` is pushed, and `release-train` with `action: rc` skips while any `wip/*` branch exists.

**Why.** A reviewed change left in a local worktree was invisible to every queue, and a release candidate was cut without it. Parking on origin makes it visible to every view the maintainer reads and to the candidate. It costs a push and an issue per parked change, and a candidate waits until the maintainer lands or deletes the branch.

**Alternatives rejected.** A local check only (`make unlanded` without the branch): the work survives only in one machine's worktree. A label on an issue without a branch: nothing holds the commits. Failing the candidate instead of skipping: a skip is the pattern of the release gate and says why on the release PR.

**Consequences.** `wip/*` is reserved for parked changes: anything else pushed there holds the candidate back. A session that ends with `make unlanded` listing its change has not finished.
