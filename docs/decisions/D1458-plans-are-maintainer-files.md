---
topic: project-governance
---

# D1458 — A plan is a maintainer's file, and the repository has no plan issues

**Decision.** A plan is a file kept outside the repository, and a maintainer handing it over in a session is its approval. The `Plan` issue template, the plan-issue branch of the skills and agents and the `approved` check go. A decision is numbered after its PR, so D672's "the plan issue's number" no longer applies to new records. The `plan` and `approved` labels stay on the closed plan issues as their history.

**Why.** D1451 left plan issues as an option for a maintainer, with no user: every plan since D1450 travelled as a file and no open plan issue is left. Keeping the branch meant each skill carrying two paths, one of them never run. The cost: approval is no longer a permission GitHub enforces but a rule the agents follow, and the design is not public before its PR, so the PR and its decision file must explain the why.

**Alternatives rejected.** Keeping the template for a public design discussion: such a discussion fits an `idea` issue, which the maintainer can answer with a plan. Removing the labels: it would detach them from the closed issues that carry them.

**Consequences.** Amends D672, D1450 and D1451. The two reviewer definitions and §3-§5 of `cartographer-review-pr` still accept a plan's issue number: their evals pass one, and a change there would require an eval run with nothing to gain. The plan's structure now lives in the `cartographer-propose-plan` skill.
