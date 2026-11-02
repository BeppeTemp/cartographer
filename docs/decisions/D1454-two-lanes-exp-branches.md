---
topic: deployment-release
---

# D1454 — Only bug fixes reach main directly; a feature lives on an exp/<theme> branch and lands in slices

**Decision.** Two lanes. The fast lane is a bug fix: `feat/<slug>` from `main`, full gate and PR, released with its candidate as any release. The long lane is a feature or experiment: an `exp/<theme>` branch made from `main` (`make worktree-add SLUG=<theme> EXP=1`), kept aligned by merging `main`, run in CI on demand (`gh workflow run ci.yml --ref exp/<theme>`) with no PR, carrying no `D<n>` until a slice has a PR, and landing on `main` in slices through normal PRs, never as a whole. A public build of the branch is a `-exp.` pre-release deleted by hand. `release-<x.y>` (D1443) stays, for emergencies only. D1384 (freeze, candidate, `rc-ok`) is unchanged and every release keeps its candidate.

**Why.** `main` held the unreleased worker while fixes were ready, which is what forced D1443's maintenance branch and cherry-picks. If long work never sits on `main` until it is ready, `main` is always releasable and a fix ships without cherry-picks. The cost: an experiment is integrated late, so the branch must merge `main` often and each slice must stand alone.

**Alternatives rejected.**
- Keep long work on `main` behind the maintenance branch: every fix then needs a cherry-pick and a hand-cut release PR.
- A release without a candidate for fixes: deferred, the candidate has not been shown to be a real cost.
- A PR per `exp/` branch for CI: it would carry the size cap and plan gate for work that is not meant to land whole; `workflow_dispatch` already runs every job.

**Consequences.** `release.yml` filters `-exp.` tags out and the candidate cleanup only touches `-rc.`, so the maintainer removes an `-exp.` pre-release and its tag. A slice's decision is numbered after its own PR (D672). Amends D1443: a maintenance release is no longer the answer to unreleased work on `main`.
