---
topic: sync-provisioning
---

# D1690 — Requirements are evaluated by one resolver over the greatest fixpoint, against what a session sees

**Decision.** `ResolveProjections` replaces `ProjectManifest` as the client-side step of `sync` and `status`. It projects
every projection of the run, then removes artifacts with an unmet `requires` until stable. An entry `<kind>:<name>` is met
by the artifact projected in any form (a degraded agent counts); a capability id is met only by `verified` for the
provider and scope of the requiring projection. A fully present cycle stays. Each removal records a cause chain, computed
against the state at the start of the round so it never depends on evaluation order.

**Visibility.** A projection sees its own set, the global projection of the same provider and the projections of its
ancestor workspaces, the last only for the kinds proven loaded from an ancestor (D1019: skills on OpenCode). Agents and
hooks in an ancestor were never probed and count as not visible. Each projection is judged against the union and loses
artifacts only from itself; a name collision is judged per projection.

**Why.** Greatest fixpoint, because requirements are a set of conditions on what is present, and a cycle among artifacts
that are all there is satisfiable. One resolver for sync and status, because a divergence would show permanent drift.
The conservative ancestor rule errs toward skipping an artifact the author asked to guard, never toward installing one
whose dependency the client may not load.

**Client side only.** Bundle and built-in artifacts are never removed. A `requires` that cannot be read is unsatisfied (the artifact is skipped, cause `requires unreadable`), not ignored: a guard that fails open silently defeats its purpose, and a sync still never fails over it.

**Judged before approval.** A requirement is evaluated on the projection, before `Apply` withholds anything for approval:
an agent awaiting approval still satisfies `agent:x`. Approval is the user's decision about running the artifact, not a
fact about what the KB declares, so it must not change what else is installed; the dependent works once it is approved,
and judging after approval would make the installed set depend on consent state that `status` computes differently.

**Full set.** `sync` and `status` both resolve every projection of the run; a projection `sync` declines to apply (its
workspace could not be prepared) still counts, since that failure is transient.
