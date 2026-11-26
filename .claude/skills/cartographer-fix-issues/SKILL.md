---
name: cartographer-fix-issues
description: Use when a maintainer asks to work through the open bugs, fix the accepted issues, empty the issue queue or take the next flaky or `ci-red` issue — takes the idle accepted bugs from `make issues`, oldest first, has each fixed by `cartographer-fix-implementer` in its own `fix-<n>` worktree, reviews it and queues it, asking the maintainer only in the cases of `CONTRIBUTING.md` §Fixes without a plan. Features go through `cartographer-propose-plan`.
---

# cartographer-fix-issues — the issue queue, one fix per accepted bug

Maintainers only. The rules are `CONTRIBUTING.md` §Issues and §Fixes without a plan: read them first.
The review and the queueing are `cartographer-implement-plan` §3 and §4, applied to one `fix-<n>`
change; this skill adds only the loop around them.

1. `make issues`. Take the lines in state `idle` of class `bug`, `ci-red` or `nightly-red`, in the
   order printed (`ci-red` and flaky first, then oldest first). Nothing else is yours: `untriaged`
   issues wait for triage, `needs-maintainer` for an answer, `fixing` for the session that has them.
2. Take one: `gh issue edit <n> --add-assignee @me`, then `make worktree-add SLUG=fix-<n>`.
3. An `accepted` `bug` opened by anyone other than a maintainer (author association OWNER, MEMBER or COLLABORATOR) or `github-actions[bot]` is outside the standing approval: it needs a maintainer's design comment and an explicit `make ready` from the maintainer. `make issues` lists it as `external`, never `idle`: it is not yours unless the
   maintainer hands it to you after that comment, and then its implementer gets the investigator's
   verified findings, never the issue number, and the maintainer queues it.
4. The cause is unknown, or the design is not in a maintainer's comment: `cartographer-investigator`
   first. When a rule of §Fixes without a plan asks the maintainer (more than one option that changes a
   public contract, a profile with standing `no` on the triage's `Profile:` line, over 150 production lines or more than one change, a third
   rejection, a breaking fix), add `needs-maintainer`, post one comment with the question and the
   options, unassign yourself and go to the next issue.
5. `cartographer-fix-implementer` on the worktree with the issue number (step 3: only for an issue a
   maintainer or `github-actions[bot]` opened).
6. Review by tier with `make check-hosted` beside it (`cartographer-implement-plan` §3), then
   `make ready` with `Closes #<n>` in the message (§4). The accepted bug is the standing approval of that
   one change: declare `standing policy of the maintainer: accepted bug #<n>` in the report, unless step 4
   applies. `make ready` copies the issue's `Found-by:` and `Introduced-by:` lines when the message has none.
7. A rejected fix (`failed/fix-<n>`, `make issues` says `rejected`) is handled as
   `cartographer-implement-plan` §4 step 4 says, twice; a third rejection is a `needs-maintainer` question.

A security defect is not in this queue: it goes to a draft advisory (`SECURITY.md`). A test in
`test/quarantine.tsv` is never queued from here: quarantining is the maintainer's call.

Report, one line per issue taken: `#<n>` and its outcome (queued as `fix-<n>`, asked, rejected), then
what `make issues` still lists as idle.
