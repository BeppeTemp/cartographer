---
topic: project-governance
---

# D1592 — Required checks run on a PR, the heavy legs only in the merge queue

**Decision.** A pull request runs the light checks: `test`, `web` (path-filtered), `pr-title` and `plan-gate`. The Windows shards and `install.ps1`, `test-macos`, `docker` and `demo-edge` run on `merge_group`, on push to `main` and on `workflow_dispatch`, never on a PR. The five required checks (`test`, `test-windows`, `web`, `pr-title`, `plan-gate`) all report on `merge_group` too.

**Why.** The account runs about five jobs at a time and a PR started about twenty (four Windows shards, install, two web legs, macOS, docker): CI queueing, not the code, set the pace of merging. The merge queue runs the full suite once per group of PRs on the commit that would land on `main`, so the cost per merged PR drops and no change reaches `main` without the heavy legs. It costs a later signal: a Windows-only break shows after the PR is queued, not on the PR.

**Alternatives rejected.**
- Keep the path-filtered heavy legs on PRs: the filters already existed and a typical PR still started a dozen jobs.
- Run the heavy legs after the merge only (as `main` did before D672): a break reaches `main` and blocks everyone.
- A second job named `plan-gate` inside `plan-gate.yml` for `merge_group`: the workflow stays `pull_request_target`-only (D955), and an event that skips a same-named job would leave a skipped check next to the real one. `plan-gate-queue.yml` is a separate no-op workflow, pinned by `TestPlanGateQueueWorkflowDoesNothing`.

**Outcome.** The merge queue could not be enabled: the ruleset API rejects the `merge_queue` rule (422), because merge queues exist only on organization-owned repositories and this one is user-owned. The maintainer kept the CI as is: a PR runs the light checks, the heavy legs run after the merge on push to `main`, and a red `main` opens or updates the `ci-red` issue, fixed by a follow-up. The `merge_group` triggers stay, harmless and ready for a move to an organization. Read "stopped in the queue" below as "caught on `main`".

**Consequences.** `test-windows` and `web` pass when their legs were skipped on a PR and fail on `merge_group` when a leg failed or was skipped. `pr-title` skips every step on `merge_group`. The `concurrency` group is the commit and never cancels a `merge_group` run. `forge` and `upgrade-e2e` stay on `pull_request` and `workflow_dispatch`. The ruleset's merge queue is enabled by the maintainer; the workflows are inert without it. The `changes` outputs `windows`, `macos`, `docker`, `demo` and `tf` are no longer read by a job but stay pinned by `ci_filters_test.go`.
