---
topic: project-governance
---

# D987 — Only the next train's work merges, and every accepted issue has a milestone

**Decision.** A milestone is a train (D956), and the open milestone due first is
the next one. A PR that closes an issue of any other milestone is blocked by the
required `plan-gate` check until the train before it has left; the check re-runs
when an issue changes milestone or a milestone is closed. Every accepted issue has
a milestone, approved plans included; an open issue without one is untriaged.
`good first issue` marks the easiest issues of the next milestones and is dropped
when nobody claims one by the Wednesday of its train week.

**Why.** The train ships whatever is on `main` when it leaves, so a milestone that
only records intent does not bound a release: work planned for a later train that
merges early ships early, which is how v0.25.0 grew to 92 commits. Gating the
merge on the milestone makes the scope of a train the maintainer's decision. It
costs latency and rebases: a reviewed PR of a later train stays open for days, and
a large change has to land as PRs that change nothing a user sees on their own.

**Alternatives rejected.**

- Milestones as intent only: the release still carries whatever merged.
- Release branches per train: the project works on `main` alone; a branch per
  train means backports and divergence for a single maintainer.
- Feature flags for unreleased work: they add runtime paths to test and remove,
  for a problem an open PR solves.
- `good first issue` issues without a milestone, waiting for a contributor: they
  sat unscheduled indefinitely; inside a milestone with a deadline they either get
  claimed or get done.

**Consequences.** The maintainer closes a train's milestone after the release,
moving what did not make it; until then the next train's PRs stay red on
`plan-gate`. A PR that closes nothing is not gated by milestone (live sessions,
chores). The rule lives once, in `triage.sh`; `plan-gate.yml` only runs it and
re-runs it on milestone events.
