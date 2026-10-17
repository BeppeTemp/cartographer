---
name: review-pr
description: Review a pull request at a bounded token cost - a scripted triage that sends a PR back without reading its code when it lacks an approved plan, is over the size cap or has red CI, then a review by the pr-reviewer subagent (and, on the full tier, security-reviewer) on a cheaper model whose findings the coordinator verifies line by line. Use when asked to review, check or merge a PR, especially a contributor's, or before opening your own PR. implement-issue and live-dev run this same procedure on the PRs they open.
---

# review-pr — review a PR without reading all of it

The rules a PR is held to are `CONTRIBUTING.md` §Pull requests (size cap,
approved plan, template) and `docs/index.md` §Documentation maintenance rules;
this skill is only the procedure. `.claude/skills/review-pr` and
`.kiro/skills/review-pr` are symlinks to this file.

## 0 — Where the cost goes

A review costs the diff plus the context needed to trust it, and the second term
is the larger one. So:

- the **design** is reviewed once, in the plan issue (a few thousand tokens),
  never rediscovered from a diff;
- the coordinator, on the most expensive model, reads the triage, the plan's work
  packages, the reviewer's findings and **only the lines those findings cite** —
  never the whole diff of a standard or full-tier PR;
- the code is read by the `pr-reviewer` subagent (full tier: also `security-reviewer`), which run on a cheaper model
  (D327 pattern: `.claude/agents/`, `.codex/agents/`, `.kiro/agents/`, same text).

## 1 — Triage (no code read)

```bash
.agents/skills/review-pr/scripts/triage.sh <pr>
```

About 15 lines: size split into production, tests, docs and generated; areas;
**tier**; the linked issues and their labels; CI; docs and decision hints;
`BLOCK` and `WARN` lines.

| Tier | When | Review |
|---|---|---|
| light | ≤ 50 production lines, outside the invariant areas | the coordinator reads the diff itself; no subagent |
| standard | more, outside the invariant areas | `pr-reviewer`, then §4 |
| full | production code in `internal/{mcpserver,provisioning,auth,gitx,sops,service}`, or any supply-chain file: a CI workflow, an installer, the release pipeline, a bundled skill | `pr-reviewer` and `security-reviewer` in parallel, then §4 plus the coordinator reads the hunks the reviewer's invariants line names, against their `AGENTS.md` — not the whole area diff. Supply-chain hunks are read whole, by the user too: no standing approval merges them |

## 2 — Send back without reading the code

Any `BLOCK` line ends the review here, with one comment that names it:

- **red CI** — nothing to review yet;
- **no approved plan** for a contributor's non-trivial change — ask for a plan
  issue (or promote the linked idea or bug in place); the design discussion
  happens there, and the maintainer approves it with the `approved` label;
- **over the size cap** without a `Size exception:` line — ask for a split by
  work package, one PR each, landing in order.

`WARN` lines (missing template section, no docs touched) go in the same comment
but do not stop the review. A comment on someone else's PR is outward-facing:
draft it, and post it on the user's word or a standing approval for the session.

## 3 — Code review by the subagent

Check the PR head out in its own worktree, never in the shared working copy:

```bash
git fetch origin pull/<pr>/head
git worktree add --detach .worktrees/pr-<pr> FETCH_HEAD
```

Spawn `pr-reviewer` with the PR number, the absolute worktree path, the tier and
the plan issue number (or "none") and the round (§5), and a `base` when the change is not against `origin/main` (default `origin/main`; §Changing the reviewer). It carries the rest. On the
full tier spawn `security-reviewer` too, in parallel on the same worktree with the
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
  `## WP` headings (`gh issue view <plan> --json body`), not with the whole
  discussion.
- `security-reviewer` findings are verified the same way. A verified high or
  medium one blocks the verdict and the merge, whatever standing approval exists;
  the rounds rule of §5 applies to it unchanged.
- Every verified finding is answered before the verdict: fixed, by a commit, or
  dismissed with a one-line reason. A `pre-existing` finding does not block and
  is not fixed in this PR: it becomes one `bug` issue each, drafted and opened on
  the user's word.
- **A `pre-existing` security finding is never made public** (it concerns released
  code): no `bug` issue, no PR comment. Draft a private GitHub security advisory
  for the user (`SECURITY.md` §Reporting). A security finding about code the PR
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

A reviewer is a definition (`.claude/agents/pr-reviewer.md` and its `.codex/` and
`.kiro/` copies, `security-reviewer` likewise), a model pin and §3–§5 of this
skill. A PR that changes any of them runs the evals and reports the score in its
`## Self-review`; nothing runs them in CI (D856: an eval is N reviewer spawns).

1. `.agents/skills/review-pr/scripts/eval-setup.sh` (or `<id>…` for some cases)
   adds one detached worktree per row of `evals/cases.md` at the PR's squash
   commit and prints one mandate line each: worktree, `base` (the commit's
   parent), plan, round 1. `--clean` removes them.
2. The coordinator spawns one `pr-reviewer` per worktree, in the background, with
   that mandate (a subagent cannot spawn one). Case `c4`, the `security` class,
   is also given to `security-reviewer`.
3. It compares each report with the row. A case is **caught** when a high or
   medium finding names the defect's file and its class; the reviewer is not
   told the row. **Noise** is the high or medium findings on the clean PRs that
   §4 verification drops.
4. One line is appended to `evals/scores.md`: date, reviewer definition commit,
   model pin, caught/cases, noise/clean. Recall comes first, noise second: a
   change that loses a case needs a reason, one that adds noise only needs to
   say so.

## When a defect escapes

A fix for a regression names its origin in the PR body, `Regression-of: #<pr>`
(`triage.sh` prints the line). When such a PR merges, the coordinator:

1. adds the introducing PR as a row of `evals/cases.md` (squash commit, fixing PR,
   defect as `file:line` at the introducing commit, expected finding, class);
2. answers in the row's last cell: what did the reviewer not have in front of it?
3. fixes that where it bites (D214): a test or a lint check, a line in the area
   `AGENTS.md`, or an entry in the reviewer's list of things to look for.

## Before opening your own PR

The same procedure moves the review cost to the author, which is the point:

1. `triage.sh` with no argument compares the current branch with `origin/main`;
   fix every `BLOCK`.
2. Spawn the reviewers of the tier (§3) on your own worktree (tier from the triage, PR number
   "not opened yet"), fix or answer each finding, in rounds as in §5.
3. Fill the PR template; its `## Self-review` section says what the review found
   and what you did with it.

## Per-client mechanics

| | How to spawn `pr-reviewer` and `security-reviewer` |
|---|---|
| **Claude Code** | `Agent` with `subagent_type: pr-reviewer` / `security-reviewer`, `run_in_background: true` |
| **Codex** | `collaboration.spawn_agent` with the `pr-reviewer` / `security-reviewer` agent |
| **Kiro** | `orchestrate_subagent` with the `pr-reviewer` / `security-reviewer` agent, one stage per PR and agent |
| **Antigravity, Copilot CLI** | no repository agent: pass the definition in `.claude/agents/pr-reviewer.md` or `security-reviewer.md` inline as the mandate |
