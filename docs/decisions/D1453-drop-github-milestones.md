---
topic: project-governance
---

# D1453 — No GitHub milestones: the version comes from the commits

**Decision.** The process uses no milestones. `release-as.yml`, `scripts/release-as.sh` and their test are removed, as are the `pr-milestone` job and the `milestoned`/`demilestoned` triggers of `plan-gate.yml` (the `plan-gate` check keeps its name, the ruleset requires it) and the milestone warning of `pr-triage.sh`. Triage is the `accepted` label (D1451). This retires D1371, the milestone parts of D1384 (`pr-milestone` and `release-as.yml` reading versions) and D987's rule that every accepted issue has a milestone.

**Why.** After D1384 (release on demand) and D1451 (contributors open issues and PRs) a milestone gated nothing and only fed two workflows, one of which needed a PAT to push and auto-merge a PR just to align a version. A roadmap per version duplicated what the `accepted` label and the release PR already say.

**What the semver bump relies on.** release-please alone, from the conventional commit types of the squash commits (`pr-title` lints them): `feat` a minor, `fix` a patch, a breaking change a minor while below 1.0 (`bump-minor-pre-major`). A larger bump than the commits give is a commit with the footer `Release-As: x.y.z`, added by hand, as the maintenance release already does (D1443).

**Alternatives rejected.** Keeping `release-as` without milestones: it had no input left. Keeping milestones as a manual roadmap: no check or workflow would read them, so they would drift.

**Consequences.** A release whose name should differ from the commits needs a manual `Release-As:` commit. The existing milestone is closed on GitHub after the release, not by this change.
