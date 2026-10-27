---
topic: deployment-release
---

# D1180 — Dependabot waits out the freeze; the open PRs are armed after the train

**Decision.** `dependabot-automerge.yml` does not arm auto-merge while the freeze
(Saturday 00:00 to Monday 08:30 Europe/Rome, D989) is open, as
`scripts/freeze-open.sh` answers. When `release-train` completes and the window is
closed, `dependabot-arm.yml` re-runs `dependabot-automerge.yml` on each open
Dependabot PR, which then applies the usual rules (D329).

**Why.** A grouped bump merged itself on Saturday after the candidate was built:
`main` no longer matched the candidate, the Monday train skipped, and a new
candidate had to be built by hand. D989 says nothing lands in the freeze unless a
maintainer merges it, and a bot's auto-merge is not that. It costs one more
workflow and a bump that waits up to two days.

**Alternatives rejected.**
- Moving Dependabot's schedule after the Monday train: a bump opened earlier (or
  rebased) still arms in a freeze, and the schedule is not the invariant.
- Arming from a step inside `release-train.yml`: that workflow deliberately runs
  no third-party action and checks nothing out, and would carry the PR rules twice.
- Re-implementing the major/docker rules in the arming workflow: re-running the
  one workflow keeps them in one place.

**Consequences.** The freeze window is defined in `freeze-open.sh` for the bots;
the train still computes its own "since last Saturday" for commits, and the two
must change together. Both workflows check out `main`, never the PR, since they
run a script with a write token.
