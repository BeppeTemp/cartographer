---
topic: project-governance
---

# D2292 — A queued fix says where its defect was found and what introduced it, and escapes feed the reviewer evals by script

**Decision.** A maintainer's queued `fix` carries a `Found-by: <value>` line per defect (the
values are `scripts/found-by.tsv`) and an `Introduced-by: <ref>` line per change that
introduced one (a commit or a landed slug of `main`, `#<n>`, or `unknown` alone), as lines of
the commit message anywhere in it; `make ready` refuses a `fix` without them
(`scripts/trailers-lint.sh`). Any queued change may carry one `Cost: impl <n>k, review <n>k,
rounds <n>` line, written by the coordinator; none must. For a queued change `Introduced-by:`
supersedes `Regression-of: #<pr>`, which stays a contributor's form and is read as the same
fact. Amends D860: a landed escape becomes a row of `evals/cases.md` by script
(`eval-case.sh --pending` lists them, `eval-case.sh <sha>` appends the row with its
hand-filled cells as `TODO`, and a test refuses a `TODO` cell); a run scores a bounded set (the
ten newest escape rows, every `security` row, the clean rows); and `make ready` refuses a
change to a reviewer definition (either reviewer agent file or the review skill's `SKILL.md`)
with no new line in `evals/scores.md` and no `Evals: none - <reason>` line, also when the
triage cannot read the caller's permission (it fails closed), while a pull request only gets a
`WARN`.

**Why.** Fixes outnumbered features and no record said which phase found a defect or which
change introduced it, so nothing could say where verification was missing. The eval loop of
D860 had stopped: its line named a pull request, and a maintainer's change has had none of its
own since trains (D1706), so no escape was added and a reviewer definition changed with no
score. The cost: every queued `fix` needs two more lines, and a typo fix in a reviewer
definition needs a recorded reason.

**Alternatives rejected.**
- Git trailers parsed with `git interpret-trailers`: a `Closes #n` line in the same paragraph
  hides the whole block, so a lint reading trailers would pass a message with no line at all.
- A check on contributors' pull requests: a contributor needs no plan and no assignment (D955),
  and cannot run the maintainer's evals.
- A blocking eval check on pull requests: a train's body is written by the lander and cannot
  carry a change's `Evals:` line, so a counted `BLOCK` would fail the train's required
  `plan-gate` check and stop the lander.
- Scoring every row: a run is N reviewer spawns, and the table only grows.
- `Cost:` required: not every client shows a subagent's usage, and a required field would be
  filled with `?` that the lint cannot tell from a measured figure.

**Consequences.** The vocabulary has one source, `scripts/found-by.tsv`; a value a script or a
workflow writes outside it fails `make test`. A commit with no `Review:` line counts as read by
a reviewer (`not recorded`), so its escapes are listed. `eval-setup.sh` reads cells by
position, so a column of `cases.md` is never inserted or moved. A fix that bundles defects of
two changes is two cases. `eval-case.sh --pending` reads all of `main` by default: a lower bound
taken from the newest row would hide an older escape not added yet.
