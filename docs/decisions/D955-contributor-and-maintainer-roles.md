---
topic: project-governance
---

# D955 — Contributor and maintainer roles: the approved-plan rule is enforced in CI

**Decision.** The role of a person or an agent is its GitHub permission: in CI
`pull_request.author_association` (OWNER, MEMBER, COLLABORATOR are maintainers),
locally `viewerPermission` on the upstream repository (ADMIN, MAINTAIN, WRITE).
A required check, `plan-gate`, runs `triage.sh --gate` on every PR and fails a
contributor's PR over 150 production lines that does not close a plan labelled
`plan` and `approved`, or that is over the size cap. The skills align with it:
agents implement only approved plans, a contributor's agent stops at an open PR,
and the reviewer agents read their instructions from the base branch.

**Why.** The gate used to exist only inside the maintainer's own review skill, so
a contributor's agent, loading the same skills, designed, implemented and opened
a PR that was refused after the work was done. A rule a cooperative agent can
skip is not a rule; GitHub is the one place it cannot be.

**Alternatives rejected.**

- Agent-side enforcement only: a skill is advice to the client that loads it, and
  a contributor controls that client.
- A `pull_request` workflow: it runs the PR's own copy of the workflow and of
  `triage.sh`, so the PR could edit the rule that judges it. `pull_request_target`
  with a base-SHA checkout runs the base branch's copy; the cost is that the
  workflow must never execute PR code, which `TestPlanGateWorkflowNeverRunsPRCode`
  pins.
- Duplicating the thresholds in YAML: `triage.sh` stays the single source, CI and
  the skills call it.
- Plan-gating maintainer PRs: live sessions land without a plan by design.
- Keeping the `idea` issue template: ideas belong in Discussions, where a
  contributor's agent has no plan to act on.

**Consequences.** `plan-gate` is advisory until the maintainer adds it to the
required checks of the `main-protection` ruleset. A `pull_request_target`
workflow added by a PR does not run on that PR, so the check starts working on
the first PR after the merge. Agent instruction files (`AGENTS.md`, agent
definitions, skills, `Makefile`, `scripts/`) count as production and supply
chain in `triage.sh`: they get the full review tier, and the user reads their
hunks. Worktrees branch from `upstream/main` when that remote exists, so a fork
clone works.
