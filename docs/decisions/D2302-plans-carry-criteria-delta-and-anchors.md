---
topic: project-governance
---

# D2302 — A plan's acceptance criteria travel in the commit message, its docs line is a delta, its pointers are anchors

**Decision.** A plan states a plan id and numbers each acceptance criterion `AC-<plan id>-<n>`, of kind `test`, `manual` or `deferred`; the change's message carries one `AC: <id> <kind> <sentence>` line per criterion of its work packages, and the test that proves a `test` criterion cites its id in a comment. The triage traces them in its branch mode (`scripts/ac-trace.sh`): a `test` criterion no test file cites is a `WARN`, never a `BLOCK`. `scripts/ac-report.sh` lists the criteria landed in a range, `manual` rows first, as the input of a release candidate's test matrix. The plan's Closing lists a docs delta, one line per current-state file and change saying what the file says afterwards, which the contract reviewer checks line by line. A plan names anchors, a file and a symbol in it, and its implementer resolves them to `file:line` on its base; an anchor that does not resolve is a stale plan.

**Why.** A plan stays outside the repository and is discarded once its changes land, so its criteria were prose that died with it: what a feature must do was written again from `git log` when a release was tested, and nothing but the reviewer's reading tied a listed edge case to a test. A commit message is what survives on `main` and what a script may read (no script reads a plan), so the criteria travel there. The trace is advisory because a criterion's sentence cannot be checked by a script, only its citation: a `BLOCK` would be satisfied by a comment, and a check that only warns costs nothing on a change that declares no criterion. The docs line of the Closing named a page, which cannot be compared with a diff; a delta can. `file:line` pointers aged whenever a sibling plan landed between the writing and the implementation, so a plan carries what does not move and the implementer derives what does. It costs the plan writer the numbering and the implementer one comment per test.

**Alternatives rejected.**
- Criteria on fixes without a plan and on live sessions: their mandate is the issue's design or the session's feedback, there is no plan to number criteria in, and a duty with no source would be filled with restatements of the title.
- A plan stored in the repository, so a script could read its criteria: a plan holds measurements and names from a real setup and the maintainer's open decisions, and is written to be discarded; the message carries only what outlives it.
- Line pointers kept in the plan: they are right on the day the plan is written and wrong after the first sibling lands, and the stale-plan stop they caused was the only defence.
- A `BLOCK` for a criterion without a test: a comment satisfies the check without the test asserting the sentence, which only the reviewer can judge.

**Consequences.**
- The `AC:` lines start at column 0 and are read anywhere in the message, not as `git interpret-trailers` trailers: a line that is no trailer in the same paragraph, such as `Closes #12`, hides the block from git.
- What counts as a test file is `kind()` of `scripts/pr-triage.sh`, which `ac-trace.sh` reads from there: the triage's size split and the trace cannot disagree.
- The trace runs in the branch mode only, where the tree is the change's; on a pull request the triage prints no criteria line.
- The plan asked that queueing a change whose `test` criterion has no citing test print the warning; `make ready` shows only the triage's `BLOCK` lines, so the warning shows where the triage is read (`scripts/pr-triage.sh`, the review's first step) and `make ready` still queues the change.
- A plan written before this decision is read item by item: a `file:line` as the anchor of the symbol there when the plan was written, a criterion without a kind as `test`, one without an id as it is written, with no `AC:` line.
