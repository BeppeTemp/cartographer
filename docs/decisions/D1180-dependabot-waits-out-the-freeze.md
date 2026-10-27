---
topic: deployment-release
---

# D1180 — Dependabot waits out the freeze; the open PRs are armed after the train

**Decision.** Dependabot's auto-merge is not armed while the freeze is open, as
`scripts/freeze-open.sh` answers: Saturday 00:00 until Monday's train has run
(Monday before 08:30, then until both scheduled `release-train` runs of the day are
completed, the rule of the implement-plan skill). `dependabot-arm.yml` disarms the
open Dependabot PRs on Saturday at 00:05 Europe/Rome, and arms them directly when a
scheduled train run completes with the freeze closed, by the rule of
`scripts/dependabot-eligible.sh` that `dependabot-automerge.yml` also uses (D329).
A failed disarm or arm fails the job and is listed in its summary.

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
- Re-running `dependabot-automerge.yml` on each PR to arm it: a re-run of a
  Dependabot-actor run may keep Dependabot's restricted token and fail quietly.
- Clock-only end of the freeze (Monday 08:30): the train often starts late, and a
  merge before it skips it.

**Consequences.** The freeze window is defined in `freeze-open.sh` for the bots;
the train still computes its own commit filter and the two
must change together. Both workflows check out `main`, never the PR, since they
run a script with a write token.
