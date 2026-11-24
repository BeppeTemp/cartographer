---
topic: project-governance
amends: D2025
---

# D2340 — A hosted run whose jobs no runner took is gated again, not red

**Decision.** In the hosted gate, a run whose non-green jobs are only jobs of `HOSTED_JOBS` that completed `cancelled` without a step having run (GitHub's "The job was not acquired by Runner of type hosted"), with no job of the run completed `failure` or `timed_out`, is runner capacity, not a verdict. `scripts/signoff.sh` reads this from the run JSON when the verdict is known, before its own `gh run cancel`, cancels the rest and ends with exit 69 and the line `signoff: the hosted runners failed (exit 0) before the gate finished: nothing pushed, nothing posted`, which `infra_line` of `scripts/land.sh` matches. The lander takes the path of D2025: the same tree is gated again, up to `LAND_INFRA_TRIES`, with no flaky issue, re-run or bisection. Any other non-green outcome stays red, and a red run now prints one `signoff: job <name>: <conclusion>` line per non-green job, so the log names it even when no Go test failed.

**Why.** A train gate was read as red because `test-macos` was cancelled for want of a runner while `gate` was green: the lander filed a flaky issue naming no test (#2313). Nothing was tested on that job, so a re-run is wasted and a flaky issue is false. The rule requires "no step ran" and "nothing failed" so a job cancelled mid-run, or a run that also has a real failure, stays red.

**Alternatives rejected.**
- Name the failed job only (the issue's other half, kept as the job lines): the lander would still file a flaky issue for a gate no test failed.
- Not filing the issue when a job was cancelled, whatever the reason: a cancellation after the job started, or beside a failure, is a verdict.
- Retry inside the signoff: the lander owns the bound and the wait (D2025).

**Consequences.**
- Amends D2025: exit 69 also means "no hosted runner was acquired", and the lander's infra line has a third alternative.
- Runners that stay unavailable stop the lander after `LAND_INFRA_TRIES` gates with the queue untouched, as for the build host.
- The hosted duration of the status is the latest `completedAt` of the run's jobs, not the run's `updatedAt`, which moves when anything touches a finished run (#2325).
- `TestSignoffHostedRunnerCapacityIsInfrastructure` and `TestLanderInfraLineMatchesTheHostedRunnersLine` pin both sides.
