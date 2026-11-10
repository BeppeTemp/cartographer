---
topic: project-governance
---

# D1877 — A queued change is pre-checked on hosted runners, and a red one is gated alone

**Decision.** The push of `ready/<slug>` runs `.github/workflows/ready-check.yml` on hosted runners: the Windows tests of the packages the change can break (`scripts/ready-check-plan.sh` over the change's own diff: `affected-pkgs.sh --code`, one, two or four shards by weight) and, when it touches `web/` or `internal/webui/`, `make web-check web-test`. The lander reads the run's conclusion on the queued commit. A train of several changes takes only changes whose pre-check is green or gives no verdict (no run within 5 minutes of the queueing, a run still going after 20, cancelled, or GitHub unreadable). When no change qualifies, the oldest change whose pre-check is still running is gated alone, else the oldest red one. A red pre-check never rejects a change by itself: the train gate on the build host stays the proof.

**Why.** On 2026-11-09, 22 trains landed 73 changes with 37 gates on the build host: 15 of them were re-runs and bisection prefixes, about 3.5 of 10.5 hours. The three bisections cost 45–53 minutes each and were started by a Windows test and a frontend unit test, the two legs an implementer's `make check` does not run. Each bisection gate is a full signoff on the one build host; the hosted runners, idle until a train merges, started 17 jobs in 3 seconds that day. Checking each change's risky legs there, in parallel and while the previous train is gated, keeps a broken change out of a train of eight, where it costs one gate of its own instead of a red train and three more.

**Alternatives rejected.**
- Reject a change whose pre-check is red: a flaky Windows test on a hosted runner would send a good change back to its author; gated alone, it lands if the build host says it is green.
- Wait for every pre-check before composing a train: an idle lander would hold a lone change for the pre-check's 8–10 minutes, although alone it risks no bisection.
- Run the whole `ci.yml` per queued change: about 17 jobs each against the account's 20 concurrent jobs, and the Linux legs repeat what `make check` and the train gate run.
- The browser suite in the pre-check: the slowest leg and the one the train gate already runs; the unit tests and bundle checks are where the red trains came from.

**Consequences.** Only a maintainer can push a `ready/` branch, so the workflow runs no pull request's code and holds no secret. A queued change waits at most until a train of others is composed: it never blocks the lander. `LAND_PRECHECK_START` and `LAND_PRECHECK_WAIT` tune the two waits. The pre-check shares the account's concurrent jobs with `ci.yml` on `main`, which may start a few minutes later on a busy day.
