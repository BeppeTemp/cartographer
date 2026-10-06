---
topic: project-governance
---

# D329 — Dependabot's patch and minor rounds merge themselves; auto-merge is on

**Decision.** The repository allows GitHub auto-merge. A workflow
(`dependabot-automerge.yml`) turns it on, squash, for every Dependabot PR whose
grouped update is a patch or minor bump; a major bump stays open for a
maintainer. Feature PRs use the same mechanism: the coordinator runs
`gh pr merge --auto` after its local gate instead of a polling loop.

**Why.** Weekly dependency PRs sat open for days with green checks because
nothing merged them, and every day open they drifted further from `main`. The
required checks already decide whether a bump is safe; a human click added no
review. With auto-merge off, landing a batch of plans also needed a background
loop per PR that had to tell "checks not registered yet" from "checks passed" —
three versions of it failed in one session.

**Alternatives rejected.**
- Auto-merging majors too: a major is where an API or behaviour changes, and
  the test suite is not a full contract for every dependency.
- A scheduled job merging green Dependabot PRs: reinvents auto-merge, with a
  delay.
- Keeping auto-merge off and merging by hand: what left the PR open.

**Consequences.** A merge made by auto-merge enabled with the workflow's
`GITHUB_TOKEN` does not trigger the push workflows on `main` (CI, release-please)
for that commit: the next merge runs them over it. A `chore(deps)` commit does
not make a release by itself, so nothing is lost but the immediate Windows run.
Auto-merge is cancelled by a push to the PR, so a rebase means re-enabling it.
