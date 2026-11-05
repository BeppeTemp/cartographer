---
topic: project-governance
---

# D1707 — One lane to main for every maintainer change; exp/ only for public builds

**Decision.** A maintainer's change, a bug fix or a slice of a feature, reaches `main` the same way: implemented on `feat/<slug>` with `make check`, reviewed on its branch, queued with `make ready` and landed by the lander in a train (D1706). A feature not ready for users lands in changes that show nothing on their own, the last one turning it on, so `main` stays releasable. An `exp/<theme>` branch exists only for a public build (a `-exp.` pre-release) of work not on `main`, and is never merged. A plan groups its work packages in a `## Changes` section, a change may build on another (`AFTER=<slug>`), and there is no cap on queued changes; the procedures (`cartographer-implement-plan`, `cartographer-develop-live`, `cartographer-propose-plan` and the implementer agents) end at a reviewed commit with its message, which the coordinator queues.

**Why.** D1454's long lane already landed each slice of a feature as its own PR made off `main`, so the `exp/` branch added a merge-`main` chore and a second place to keep aligned without changing what reached `main`. The rules that kept the PR flow manageable (at most 4 open maintainer PRs, no PR depending on another open one) serialised independent and dependent work alike, on a gate that a train now pays once for up to 8 changes. With the queue doing the ordering and the gating, the lanes and caps only cost waiting. It costs discipline on `main`: a half-built feature must be invisible until its last change, which a review has to check, where an `exp/` branch kept it out by construction.

**Alternatives rejected.**
- Keep the two lanes, landing slices through the queue: the `exp/` branch still needs aligning and is still not what lands; it buys nothing the dark landing on `main` does not.
- Keep the 4-PR cap for queued changes: the train caps the gating unit at 8 already, and a cap on the queue only moves the waiting from the lander to the sessions.
- Rename the procedures (one `deliver` skill, one implementer agent): the steps changed, the roles did not; renames would only break the habits and the references.

**Consequences.** Supersedes D1454's two lanes (its `-exp.` public build and D1443's `release-<x.y>` for emergencies stay). An implementer never pushes, queues or opens a PR; its end is a commit and a message file. A contributor's path is unchanged: `make gate`, a PR, CI, squash merge.
