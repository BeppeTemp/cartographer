---
topic: project-governance
---

# D1610 — A maintainer's PR is attested by a local signoff, the hosted checks run once

**Decision.** A maintainer pushes with `make signoff`: it runs the gate (and the web legs when the diff needs them), pushes the branch with an explicit remote and `--force-with-lease`, and posts a `signoff` commit status on the pushed sha. On a maintainer's PR (`author_association` OWNER, MEMBER or COLLABORATOR, not a bot, not a `release-please--*` head) `test` and the web legs are skipped, and the new job `gate` passes iff a `signoff` status exists on the head sha, created by someone with admin, maintain or write access. A contributor's, a bot's and the release PR still run `test` and the web legs, and `gate` collects them. The required checks become `gate`, `pr-title` and `plan-gate`; `test-windows` and `web` no longer run on a PR. Runs on `main` coalesce (one running, the newest pending, per workflow; `privacy.yml` and `release-please.yml` excepted), and `main-status` names the range since the last green push run. `privacy.yml` drops `synchronize`; CodeQL leaves the PR and runs on `main`, weekly and on demand. A maintainer has at most four open PRs, none depending on another open PR, and a plan groups its work packages into the fewest PRs.

**Why.** The account gets one or two hosted runners at a time. A push to `main` started about 22 jobs and a maintainer PR about 9, re-run after every rebase, although `make gate` had already verified it locally. Moving the proof of a maintainer's PR to a status posted after a local run leaves one hosted check to wait for. It costs trust in the maintainer's machine for that PR, and the four post-merge targets (`smoke-http`, `smoke`, `e2e`, `test-install`) run for a maintainer's change only after the merge; the signoff targets are one variable at the head of `scripts/signoff.sh`. This amends D1592 (five required checks, "the concurrency group is the commit" on push, `test`/`web` on every PR) and D1020 on one point: CodeQL no longer runs on a PR; the dependency scans and the workflow lint still run on a PR that changes the dependency graph or the workflows.

**Alternatives rejected.**
- A merge queue: not available on a user-owned repository (D1592).
- Keep `test` required and skip it for maintainers: a job skipped by `if:` reports as passed, so it would prove nothing. For the same reason `gate` has no `if` but `!cancelled()`, and a test pins it.
- Keep `synchronize` in `privacy.yml`: the `push` run scans the same commits, a rebase included.
- Classify the author by the PR's branch name or a label: only `author_association` cannot be forged by the PR; the release PR is opened by the maintainer's token, so its head ref is excluded explicitly.

**Consequences.**
- Between the merge of this change and the ruleset switch `gate` is not required while `test` is skipped on a maintainer's PR: nothing else merges in that window, and open PRs are rebased onto the new `ci.yml` before the switch (a PR on the old file never reports `gate`).
- A commit pushed after a signoff has no status and fails `gate`; `make signoff` posts it and re-runs a failed `ci.yml` run of the sha.
- A Windows- or macOS-only break of a maintainer's change is caught on `main` and tracked by `ci-red`, as D1592 already accepted.
- `gate` must never get an `if` that can skip it.
