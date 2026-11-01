---
topic: project-governance
---

# D1455 — The 0.28 features leave main and wait on exp branches

**Decision.** `main` holds 0.27 plus its fixes, CI and docs. The 36 `feat` commits merged since v0.27.0 are reverted by one PR, one revert commit each, and stay preserved on `exp/0.28`; each theme lands again later from its own `exp/<theme>` branch, in slices.

**Why.** The features were merged faster than they could be judged as a release, and `main` is what the weekly release is cut from. `main` is protected, so removing them is a revert PR, not a history rewrite. Cost: the features' history stays in the log and a later slice re-applies the work instead of cherry-picking a green commit.

**Alternatives rejected.** Force-pushing `main` to the 0.27 line: forbidden by the protection and it would orphan every open PR. Reverting by hand into one commit: it loses the per-feature pointer that lets a slice be re-applied by reverting its revert. Shipping 0.28 as it stood: the maintainer chose not to.

**Consequences.** A fix made on top of a reverted feature went with it (its content is on `exp/0.28`); a fix to code that survives stayed. The reverted commits are still in the range since v0.27.0, so release-please still sees the `feat` commits that the `Revert` commits do not cancel: the release PR's version and changelog need the maintainer's attention (PR body).
