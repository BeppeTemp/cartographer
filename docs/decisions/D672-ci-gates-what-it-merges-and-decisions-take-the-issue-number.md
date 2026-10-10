---
topic: project-governance
---

# D672 — CI gates what it merges, and a decision takes its issue's number

**Decision.** Four changes to how work lands.

- `test-windows` runs on a feature PR that touches a package with
  platform-specific code (`cmd/`, `internal/{service,provisioning,configurator,clientconfig,agents,execbit,gitx,kb,repoindex,defaults}`,
  the installers, any `*_windows.go`), on every push to `main` and on the
  release PR. This partly reverses D328.
- `web` runs on every push to `main` as well as on the PRs that touch its paths,
  and it is a required check, along with `pr-title`. A `main-status` job keeps
  one `ci-red` issue open while `main` is red and closes it on the next green
  push.
- The `pages` concurrency group covers only the docs deploy job, not the build.
- A decision record is written only for an architectural or contract choice,
  and its number is the number of its plan issue, or of its PR if it has no
  plan. Idea and bug issues have templates of their own. When one of them is
  picked up, it is promoted in place to a plan.

**Why.** Over the 300 CI runs between 2026-10-06 and 2026-10-10, 93 failed, and
nearly all of them came from two gaps. The first is that Windows did not run on
PRs. A test that did not compile on Windows reached `main` and kept 22
consecutive pushes red, along with the release PR, for two days. `make vet` now
catches compile errors, but runtime failures were caught only after the merge.
Every Windows fix in the log so far is in the packages listed above. The second
gap is `web`, which was not required and did not run on `main`. A PR with a
failing browser suite was merged, and the next unrelated PR inherited the
failure. Separately, a deploy left waiting with no reviewer held the `pages`
group, which cancelled every docs run, PR builds included, for four days.

On the process side, reserving a decision number meant cross-checking the files
on disk against every plan title, and parallel sessions could still collide. A
record for every "non-obvious" choice had made the decision log longer than it
was useful. Closing a request issue and filing a new plan for the same work
split one discussion across two issues.

**Alternatives rejected.**

- Running Windows on every PR: 8-13 minutes on every PR, which was D328's
  original complaint.
- Running Windows on PRs without making it required: auto-merge waits only for
  required checks, so a red result would not block the merge, as happened with
  `web`.
- A merge queue: not available for this repository (D328).
- Keeping sequential decision numbers with a reservation helper: the race is
  still there, only narrower.

**Consequences.** A Windows regression in a package outside the list still
reaches `main`. It then opens a `ci-red` issue rather than sitting unnoticed
until the release PR. When a platform-specific file appears in a new package,
that package goes into the `windows` filter in `ci.yml`. Decision numbers jump
from D371 to issue numbers. The numbers in between are unused, not reserved,
and need no `GapDecisions` entry, because nothing cites them.
