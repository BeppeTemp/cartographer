---
topic: project-governance
---

# D856 — A pull request is shaped to be cheap to review

**Decision.** Three rules bound what a reviewer has to read, and one skill
bounds how much of it the most expensive model reads.

- A contributor's change over 150 production lines, or one that changes a public
  contract, closes a plan issue the maintainer approved: labelled `plan` and
  `approved`. The template sets `plan` for anyone; only someone with triage
  rights can set `approved`.
- At most 500 production lines added per PR (tests, docs and generated files
  excluded); a larger change is split by work package, or the body states a
  `Size exception:`.
- The body follows `.github/pull_request_template.md`, including a
  `Self-review` section: the author runs an independent review before opening.

The `review-pr` skill applies them: a script (`triage.sh`) reads only the PR's
metadata and sends it back on a `BLOCK`; past that, the `pr-reviewer` subagent,
on a cheaper model and read-only, reads the code in a worktree at the PR head and
returns at most ten findings with the lines that prove them; the coordinator reads
those lines, not the diff. Light PRs (≤ 50 production lines outside the invariant
areas) skip the subagent.

**Why.** With a second contributor, review became the bottleneck: ten PRs in a
day, about 7,200 lines added, one of them 2,800 lines in 25 files. Reviewing
that on the most expensive model cost about as much as writing it. The cost of a
review is the diff plus the context needed to trust it, and the second term is
the larger one: a design decided inside a PR has to be rediscovered from the
diff. Approving the design in a plan costs a few thousand tokens once; after
that the review is conformance to the plan plus the invariants. The size cap
exists because the cost of reading grows faster than the diff: the reader has
to hold more of it at once. The self-review moves the first pass to the author's
budget, where it belongs.

**Alternatives rejected.**

- A size check in CI: a non-required failing check is noise, and a required one
  blocks the exceptions the rule allows. The triage script gives the same answer
  to the author before opening and to the reviewer after.
- A review bot in CI: it spends API budget on every push, including the ones a
  `BLOCK` would have sent back without reading.
- Applying the plan rule to the maintainer's own PRs: those already come from a
  plan issue or a live session (D703).

**Consequences.** The `approved` label exists on the repository. `triage.sh` and
`CONTRIBUTING.md` §Pull requests carry the same two numbers and change together.
`pr-reviewer` is a third repository subagent in three formats, kept identical by
`TestRepoSubagentsSayTheSameThingOnEveryClient`.
