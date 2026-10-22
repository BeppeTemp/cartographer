---
topic: project-governance
---

# D1058 — A PR assigned to its author claims the plan

**Decision.** For a contributor's PR, `plan-gate` accepts the approved plan it
closes when the plan is assigned to the PR author or the PR is assigned to its
author. Both are maintainer acts: only triage rights can set assignees.

**Why.** GitHub silently ignores an assignment to a user who never commented on
the issue. A contributor who opened a PR without first claiming the plan was
blocked by a gate that asked for an assignment the maintainer could not make;
the PR author, instead, can always be assigned to their own PR. The cost: the
plan itself may stay unassigned, so it no longer shows on its own who holds it.

**Alternatives rejected.**
- A maintainer's approving review as the claim: `pull_request_review` has no
  `_target` variant, so on a fork PR it would run the PR's own workflow copy.
- A new maintainer label on the PR: one more label to learn for the same act
  an assignment already expresses.
- Asking the contributor to comment first: it is the rule already, and the
  trap is precisely the PR that arrives without it.

**Consequences.** `plan-gate.yml` re-runs on `assigned`/`unassigned` of the PR
(pinned by `internal/repodocs/plan_gate_test.go`). The plan-assignment rule of
D955 stays the primary path; the PR assignment is the fallback.
