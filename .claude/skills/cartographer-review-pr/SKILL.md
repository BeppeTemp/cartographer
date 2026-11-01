---
name: cartographer-review-pr
description: "Use when asked to review or check a pull request, especially a contributor's, or before opening your own PR — runs a scripted triage that sends a PR back without reading its code when it lacks an approved plan, is over the size cap or has red CI, then a review by `cartographer-contract-reviewer` (full tier: also `cartographer-security-reviewer`) on a cheaper model, whose findings the coordinator verifies line by line. `cartographer-implement-plan` and `cartographer-develop-live` run this same procedure on the PRs they open; merging is `cartographer-implement-plan` §4."
---

# cartographer-review-pr — review a PR without reading all of it

The rules a PR is held to are `CONTRIBUTING.md` §Pull requests (size cap,
approved plan, template) and `docs/index.md` §Documentation maintenance rules;
this skill is only the procedure.

## Role

Before anything else, find your role (D955; `CONTRIBUTING.md` §Working with an agent client):

```bash
gh repo view "$(scripts/upstream-repo.sh)" --json viewerPermission --jq .viewerPermission
```

`ADMIN`, `MAINTAIN` or `WRITE` is **maintainer mode**; `TRIAGE`, `READ`, or any
failure to read it, is **contributor mode**. State the mode to the user in one line.

**Contributor mode** runs only §Before opening your own PR: triage and
self-review. It never sends a `gh pr review` verdict, never edits labels, never
files issues for others and never drafts advisories (§4, §2 comments). Maintainer
mode is the whole skill.

## 0 — Where the cost goes

A review costs the diff plus the context needed to trust it, and the second term
is the larger one. So:

- the **design** is reviewed once, in the plan issue (a few thousand tokens),
  never rediscovered from a diff;
- the coordinator, on the most expensive model, reads the triage, the plan's work
  packages, the reviewer's findings and **only the lines those findings cite** —
  never the whole diff of a standard or full-tier PR;
- the code is read by the `cartographer-contract-reviewer` subagent (full tier: also `cartographer-security-reviewer`), which run on a cheaper model
  (`.claude/agents/`, pinned in each definition). Spawn them with `Agent`,
  `subagent_type: cartographer-contract-reviewer` / `cartographer-security-reviewer`,
  `run_in_background: true` (`cartographer-source-reader` the same way).

## 1 — Triage (no code read)

```bash
scripts/pr-triage.sh <pr>
```

About 15 lines: size split into production, tests, docs and generated; areas;
**tier**; the linked issues and their labels; CI; docs and decision hints;
`BLOCK` and `WARN` lines (a PR title scope `pr-title` refuses included).

| Tier | When | Review |
|---|---|---|
| light | ≤ 50 production lines, outside the invariant areas | the coordinator reads the diff itself; no subagent |
| standard | more, outside the invariant areas | `cartographer-contract-reviewer`, then §4 |
| full | production code in `internal/{mcpserver,provisioning,auth,gitx,sops,service}`, or any supply-chain file (`SUPPLY_CHAIN` in `scripts/pr-triage.sh`: the CI workflows, the installers, the release pipeline, the bundled skills, `AGENTS.md`/`CLAUDE.md`, `SECURITY.md`, `CONTRIBUTING.md`, `.claude/`, the Makefile, `scripts/`, the issue and PR templates) | `cartographer-contract-reviewer` and `cartographer-security-reviewer` in parallel, then §4 plus the coordinator reads the hunks the reviewer's invariants line names, against their `AGENTS.md` — not the whole area diff. Supply-chain hunks are read whole, by the user too: no standing approval merges them |

## 2 — Send back without reading the code

Any `BLOCK` line ends the review here, with one comment that names it:

- **red CI** — nothing to review yet. On a contributor's PR that is the whole
  comment; on your own PR, spawn `cartographer-source-reader` with the PR number and fix from its
  digest instead of opening the log;
- **no approved plan** for a contributor's non-trivial change — ask for a plan
  issue (or promote the linked bug in place); the design discussion
  happens there, and the maintainer approves it with the `approved` label;
- **over the size cap** without a `Size exception:` line — ask for a split by
  work package, one PR each, landing in order.

`WARN` lines (missing template section, no docs touched, a mapped page not touched) go in the same comment
but do not stop the review. A comment on someone else's PR is outward-facing:
draft it, and post it on the user's word or a standing approval for the session.

## 3 — Code review by the subagent

Check the PR head out in its own worktree, never in the shared working copy:

```bash
git fetch origin pull/<pr>/head
git worktree add --detach .worktrees/pr-<pr> FETCH_HEAD
```

Spawn `cartographer-contract-reviewer` with the PR number, the absolute worktree path, the tier and
the plan issue number, the absolute path of a plan file or "none", and the round (§5), the triage's docs lines (`WARN` mentioning `not touched`, `Docs: none`) verbatim, and a `base` when the change is not against `origin/main` (default `origin/main`; §Changing the reviewer). It carries the rest. On the
full tier spawn `cartographer-security-reviewer` too, in parallel on the same worktree with the
same mandate: both are read-only. Standard and light tiers get no security pass,
for cost. Several PRs can be reviewed in parallel, one worktree and one pair of
subagents each, in the background.

## 4 — Verify, then decide

- For every finding, read the cited lines yourself (`sed -n '<a>,<b>p' <file>` in
  the PR worktree) and drop what does not hold: the report is a lead, not
  evidence.
- Full tier: read the ranges of the reviewer's invariants line (`none` means none),
  never the whole diff of the area — most PRs touch `internal/mcpserver`, and
  reading all of it is the cost this skill exists to avoid.
- Plan conformance: compare the reviewer's work-package line with the plan's
  `## WP` headings (`gh issue view <plan> --json body`, or the plan file), not with the whole
  discussion.
- `cartographer-security-reviewer` findings are verified the same way. A verified high or
  medium one blocks the verdict and the merge, whatever standing approval exists;
  the rounds rule of §5 applies to it unchanged.
- Every verified finding is answered before the verdict: fixed, by a commit, or
  dismissed with a one-line reason. A `pre-existing` finding does not block and
  is not fixed in this PR: it becomes one `bug` issue each, in the open milestone,
  and is fixed in the same session when it is bounded (`CONTRIBUTING.md` §Fixes
  without a plan). A flaky or broken test met on the way is one too: a rerun that
  turns it green is not an answer.
- **A `pre-existing` security finding is never made public** (it concerns released
  code): no `bug` issue, no PR comment. Draft a private GitHub security advisory
  for the user (`SECURITY.md` §Reporting a vulnerability). A security finding about code the PR
  adds may go in the review: that code is not released.
- The verdict is one review, `gh pr review <pr> --approve` or `--request-changes`
  with the verified findings as `file:line` bullets — drafted, posted on the
  user's word.
- Merging stays the user's decision: squash, on green CI, after the approval.
- `git worktree remove .worktrees/pr-<pr>` when the PR is closed or merged.

## 5 — Rounds converge

An author agent and a reviewer agent can keep each other busy indefinitely, and
every finding costs the author a commit and the reviewer another round (D857):

- **At most three rounds** per PR. Round 1 reviews `origin/main...HEAD`. A later
  round gets the commit the previous round reviewed and its findings with their
  answers, reads only the new commits, and reports only what they introduce.
- **Light tier: one round**, the coordinator's own read.
- A high or medium finding still open after round 3 says the approach is the
  problem, not the next fix: stop and hand it to the user (on a contributor's PR,
  `--request-changes` saying so).
- A finding the reviewer has to make in two PRs becomes a rule where it bites —
  a test or a lint check, else a line in the area `AGENTS.md` or in this skill
  (D214) — so that no review has to make it a third time.

## Changing the reviewer

A reviewer is a definition (`.claude/agents/cartographer-contract-reviewer.md`, `cartographer-security-reviewer` likewise), a model pin and §3–§5 of this
skill. A PR that changes any of them runs the evals and reports the score in its
`## Self-review`; nothing runs them in CI (D856: an eval is N reviewer spawns).

1. `.claude/skills/cartographer-review-pr/scripts/eval-setup.sh` (or `<id>…` for some cases)
   adds one detached worktree per distinct squash commit of `evals/cases.md`
   (cases on the same commit share it, as `eval-c2-c3`) and prints one mandate
   line each: the case ids it serves (`c2,c3`), worktree, `base` (the commit's
   parent), plan, round 1, and an `eval:` constraint. `--clean` removes them.
2. The coordinator spawns one `cartographer-contract-reviewer` per mandate line, in the background,
   with that line verbatim (a subagent cannot spawn one). The worktree holds the
   whole history, so the `eval:` constraint is what keeps the reviewer from
   finding the fix: both reviewer definitions honour it (no history after HEAD,
   no reading of `evals/`, no `gh` lookup but the plan). Case `c4`, the
   `security` class, is also given to `cartographer-security-reviewer`.
3. It compares each report with the rows; the reviewer is not told them. A case
   is **caught** when a high or medium finding names the defect's file and its
   class; a shared run is scored once per case it serves. **Noise** is the high
   or medium findings on the clean PRs that §4 verification drops.
4. One line is appended to `evals/scores.md`: date, reviewer definition commit,
   model pin, caught/cases, noise/clean. Recall comes first, noise second: a
   change that loses a case needs a reason, one that adds noise only needs to
   say so.

## When a defect escapes

A fix for a regression names its origin in the PR body, `Regression-of: #<pr>`
(`pr-triage.sh` prints the line). When such a PR merges, the coordinator:

1. adds the introducing PR as a row of `evals/cases.md` (squash commit, fixing PR,
   defect as `file:line` at the introducing commit, expected finding, class);
2. answers in the row's last cell: what did the reviewer not have in front of it?
3. fixes that where it bites (D214): a test or a lint check, a line in the area
   `AGENTS.md`, or an entry in the reviewer's list of things to look for.

## Before opening your own PR

The same procedure moves the review cost to the author, which is the point:

1. `scripts/pr-triage.sh` with no argument compares the current branch with `upstream/main`
   (`origin/main` without that remote) and prints your role; fix every `BLOCK`.
   The `plan-gate` check runs the same script on the PR.
2. Spawn the reviewers of the tier (§3) on your own worktree (tier from the triage, PR number
   "not opened yet"), fix or answer each finding, in rounds as in §5.
3. Fill the PR template; its `## Self-review` section says what the review found
   and what you did with it.
