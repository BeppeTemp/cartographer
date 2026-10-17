---
topic: project-governance
---

# D860 — A defect that escapes review becomes an eval case for the reviewers

**Decision.** A `fix:` PR that repairs a regression names the PR that introduced
it, `Regression-of: #<pr>`. That PR becomes a row of
`.agents/skills/review-pr/evals/cases.md`, and a change to a reviewer definition,
its model pin or `review-pr` §3-§5 is scored on the rows (and on clean PRs, for
noise) before it lands; the score goes in `evals/scores.md` and in the PR's
`## Self-review`. A case is caught when a high or medium finding names the
defect's file and its class. Recall comes first, noise second.

**Why.** Reviewer definitions and model pins change, and nothing said whether
they still catch the bugs that matter; a defect that escaped left no trace that
could be learned from. Every miss now gets one question, "what did the reviewer
not have in front of it?", answered where it bites (D214).

**Alternatives rejected.** Running the evals in CI: D856 rejected a review bot
for its cost, and a run is N reviewer spawns. A script that spawns the reviewers:
a subagent cannot spawn one, so the coordinator drives and the script only
prepares worktrees and mandates. Linking fixes to origins by `git blame` at fix
time only: it is a one-off harvest (used for the seed), not a link the next
reader can follow.

**Consequences.** `pr-reviewer` and `security-reviewer` take an optional `base`
(default `origin/main`) because a case is a past PR. The seed set is small and
dated; it is extended by escapes, not curated. A score is a count over a handful
of cases: read it as a regression alarm, not a measurement.
