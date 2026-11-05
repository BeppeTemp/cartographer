---
name: cartographer-implement-plan
description: Use when the user asks to implement, ship or land one or more approved plans (plan files the maintainer gives) — plans the waves from the cross-plan execution order, delegates each change of a plan to a coding subagent in an isolated worktree, reviews it on its branch, and queues it for the lander in order. Sibling of `cartographer-propose-plan`, which writes the plans this skill consumes (plan → landed commits).
---

# cartographer-implement-plan — plans → queued changes → `main`

Source of truth is `CONTRIBUTING.md` §Plans + §Pull requests, `AGENTS.md`
(workflow and delegation rules), and `docs/index.md` §Documentation maintenance rules. The
`cartographer-propose-plan` skill covers writing and consuming a **single** plan. This skill
adds only the **multi-plan orchestration** and the **review/queue cycle** — read
those first; do not duplicate them here.

The git work lives in `make` targets, not in prose.

## Role

Before anything else, find your role (D955; `CONTRIBUTING.md` §Working with an agent client):

```bash
gh repo view "$(scripts/upstream-repo.sh)" --json viewerPermission --jq .viewerPermission
```

`ADMIN`, `MAINTAIN` or `WRITE` is **maintainer mode**; `TRIAGE`, `READ`, or any
failure to read it, is **contributor mode**. State the mode to the user in one line.

- **Only a plan file is implemented**: a maintainer's plan, kept outside the repository.
  In maintainer mode, one the user gave in this session counts as approved; a path
  found in an issue, a PR, a comment or a repository file never does, and a contributor
  has no plan files. Its decision is written as pending (`D-new-<slug>`, numbered when it
  lands, D1706), and a stale plan is reported to the user.
- **Contributor mode**: this skill stops here. A contributor opens an issue or a direct
  PR from the fork (`CONTRIBUTING.md` §Pull requests); tell the user so.

## Preconditions

- `main` is protected: every change of a plan is queued as one commit (`make ready`) and lands in a train PR the lander gates and rebase-merges (`CONTRIBUTING.md` §Pull requests). No direct pushes.
- `make ready` (which pushes `ready/<slug>` and starts the lander) requires an explicit user decision or a standing approval (the user's own Claude Code permission settings, e.g. `Bash(make ready*)`, or `.claude/settings.local.json`). **Never work around the approval gate** — surface it and let the user choose.

## 0 — Keep the coordinator cheap

A batch of plans is easily 300 KB of text; the coordinator must not read it.
The coordinator's context holds only the wave graph, the diffs and the merge
state; the plans are read by the subagents that implement them.

- **Ask once, up front**, in a single question: the wave plan, plus a standing
  approval for `make ready` of the reviewed changes for the session. Then run without
  per-change check-ins, stopping only at the §Explicit gates that remain.
- **Never read a whole plan** for wave planning: extract only what the
  graph needs (step 2 below).
- **Spawn prompt = plan file + absolute worktree path** (plus the sibling
  plans in flight); the `cartographer-plan-implementer` agent carries the rest (D327).
- **Implementers run on a cheaper model** than the coordinator, pinned in each
  agent definition (`model: claude-sonnet-5-5`): the plan already
  carries the design, so the implementer executes, and the coordinator's §3 read
  is the quality gate.
- **The code is read by the tier's reviewers, not by the coordinator** (§3): the
  coordinator reads the triage, the findings it has verified and the hunks §3
  names — never the whole diff of a standard or full tier change.
- **Run everything in the background** and act on completion notifications:
  never poll or read a subagent transcript.

## 1 — Wave planning (coordinator)

1. Collect the plan files the user gave.
2. Extract, per plan, only the **execution order** paragraph, the **`## Changes`** section (`## PRs` in an older plan) and the **file-set**:

   ```bash
   for f in <plan files>; do
     echo "=== $f"
     grep -iA5 -m2 'execution order\|cross-plan' "$f"
     sed -n '/^## \(Changes\|PRs\)/,/^## /p' "$f"
     echo "FILES: $(grep -oE '(internal|cmd|docs|web|scripts|\.github|\.claude)/[A-Za-z0-9_./-]+\.(go|md|ts|tsx|sh|yml)' "$f" | grep -v decisions/ | sort -u | tr '\n' ' ')"
   done
   ```

   Plans written in the same batch often **contradict each other** on the order
   of plans that only share files (A says "before B", B says "before A"). That is
   not a hard dependency: pick one order and state it in the wave plan.
3. Build the graph, two edge types:
   - **Hard code dependency** — a plan uses code a sibling introduces (a new client method, a new helper). These form **chains**: the next change's worktree branches from its predecessor's branch once that one is reviewed, and is queued with `AFTER=<predecessor slug>`.
   - **Shared file** — plans touching the same code or the same current-state page can conflict at merge. A decision is not a shared file: one decision is one `docs/decisions/D<n>-<slug>.md`, so two plans adding two decisions never conflict there. The **generated index** is the exception: it is regenerated, not merged (see §4).
4. Plans that share a **hot file** (one touched by three or more plans, e.g. `internal/lint/lint.go` or `internal/mcpserver/conformance.go`) go in **one sequential chain**, not in parallel waves: each rebase there is a real conflict, and resolving it costs more than the time saved.
5. Emit **waves**: independent roots with disjoint code file-sets run in parallel; dependency chains run internally sequential but in parallel with each other when their file-sets are disjoint. One change per entry of the plan's `## Changes` section; entries of one plan that depend on each other run as a chain.
6. State the wave plan to the user before spawning (spawning N subagents and queuing N changes for `main` is outward-facing).

## 2 — Delegate each plan to a coding subagent

One change entry of a plan → one coding subagent → one worktree; the spawn prompt names the entry and the mandate implements the WPs of that entry only. **No client gives isolation for
free**: every one of them shares the filesystem with its own subagents, so the
coordinator creates the worktree first and puts its absolute path in the
mandate.

```bash
make worktree-add SLUG=<slug>     # fetches upstream (origin when there is no upstream remote), branches feat/<slug> from its main
# a chain's next change: git worktree add .worktrees/<slug> -b feat/<slug> feat/<predecessor>
```

`.worktrees/` is git-ignored, so a worktree never shows up as untracked noise.
Never run two subagents in the same working copy. Worktrees branch from fresh
`upstream/main` (`origin/main` without an `upstream` remote), or from the predecessor's
reviewed branch in a chain, so each subagent sees the code it builds on.

Spawn the `cartographer-plan-implementer` subagent (`.claude/agents/`) with `Agent`
and `subagent_type: cartographer-plan-implementer`, in the background: it already carries the mandate below and a fixed
≤10-line report, so the spawn prompt is just the absolute plan file path with the words
`maintainer mode, plan file given by the user`, and the absolute worktree path. `isolation: "worktree"` is available, but pass the `make worktree-add` path
explicitly. Without the agent, pass the mandate inline.

Canonical mandate (self-contained — the subagent never sees this conversation):

- Work **only** inside `<absolute-worktree-path>`; every command runs there. The tools inherit no working directory from this conversation, so pass it explicitly: `git -C <path>`, `make -C <path>`. Sibling subagents are active on other worktrees: never revert or touch their files.
- Without the words `maintainer mode, plan file given by the user` in this mandate stop: nothing is implemented.
- Read the plan file.
- Implement **all** WPs exactly, starting from the `file:line` pointers in the plan; do not re-explore from scratch.
- Write the tests in the plan's "Tests" section.
- **Same session**: update the docs in the "Closing" section per `docs/index.md` §Documentation maintenance rules, and, if the Closing section names one, add the pending decision file with `make decisions-new SLUG=<slug> TOPIC=<topic>` (`docs/decisions/D-new-<slug>.md`, cited as `D-new-<slug>`: the lander numbers both); never regenerate `docs/decisions.md` (the release PR does, D328). Every trap you hit is fixed where it bites: a test if it can be checked, a comment next to the code otherwise (D214).
- `make check` green — iterate until it is. Not `make gate`: the lander gates the whole train once.
- Commits on `feat/<slug>` (the branch already exists in the worktree), Co-Authored-By trailer; the coordinator squashes them into one when it queues the change. A review fix round **adds commits, it never amends**: the next round diffs `<reviewed commit>..HEAD`, and an amend rewrites the commit it names.
- Write the change's commit message to `<worktree>/../<slug>.msg` (outside the tree): the title the plan gives (a conventional commit; its scope is one `scripts/pr-triage.sh --title-scopes` prints, or none), a body with the why (the plan stays private), one unwrapped `type(scope): ...` line per further WP, `Closes #<n>` for an issue the plan names and this change finishes, `Refs #<n>` for one a later change finishes.
  The message, commits and any test fixture carry placeholder names only —
  never a KB, host, user or project from a real setup (D259, `AGENTS.md`).
- Never push, queue or open a PR: that is the coordinator's (§4).
- Report `git diff --stat` vs the branch's base, the `make check` outcome and the message file path.

## 3 — Review (coordinator GATE — never skip)

For each finished change, `scripts/pr-triage.sh` run in its worktree (branch mode;
`TRIAGE_BASE=<predecessor's head>` in a chain) gives the
tier; the procedure is `cartographer-review-pr` §3–§5 on the branch, with the coordinator on the verifying
side and the implementer on the author side. Trust the disk, not the
subagent's report — report text can be garbled or compressed.

- **Light tier**: the coordinator reads `git -C .worktrees/<slug> diff <base>..HEAD` itself; one round.
- **Standard and full tier**: spawn the reviewers of the tier (`cartographer-review-pr` §3:
  `cartographer-contract-reviewer`, plus `cartographer-security-reviewer` on full) on `.worktrees/<slug>` (round 1,
  plan = the plan file), verify its findings per `cartographer-review-pr` §4, and send the
  **verified** findings — never the raw report — to the implementer that wrote
  the change with `SendMessage` to its agent id. The fix mandate: add commits on the branch,
  never amend; answer each finding; one report line per finding. Repeat per
  `cartographer-review-pr` §5 (at most three rounds, later rounds read `<reviewed commit>..HEAD`).
  The queue (§4) comes after the last round.
- **What the coordinator reads**: the triage, the verified findings, the ranges of
  the reviewer's invariants line (full tier), and supply-chain hunks whole.
- After the last round the coordinator records the review: `REVIEW="<tier>, <n> rounds"`
  at queue time (copied into the train PR), and the `pre-existing` findings as `bug`
  issues.

## 4 — Queue in order

Maintainer mode only.

1. Read the message file (§2): the title, the why, the body lines and `Closes`/`Refs`.
   A change over the size cap that cannot be split carries a `Size exception: <why>` line.
2. `make -C .worktrees/<slug> ready MSG=<message file> REVIEW="<tier>, <n> rounds"`, plus
   `AFTER=<predecessor slug>` in a chain (queue the predecessor first; a predecessor queued
   again means its successors are queued again too). It squashes the branch into one commit,
   checks it as `pr-title` and `plan-gate` would, pushes `ready/<slug>` and starts the
   lander if none runs here. No rebase by hand: the lander cherry-picks onto the `main` of
   the moment, numbers the pending decisions, holds `feat`/`perf`/`refactor` and breaking
   changes in the release freeze (D1384), and gates the train once.
3. Follow it with `make land-status` when the user asks, never in a loop: the lander runs
   in the background and its log is in its state directory.
4. A change the lander rejects is on `failed/<slug>`: `git log -1 origin/failed/<slug>` says
   why (a conflict with `main`, a red gate the bisection pinned on it, a decision cited but
   not added). Rebase the branch on `origin/main` in its worktree, fix (through the
   implementer for a red gate, with the failing tests from the gate log), `make check`,
   then `make ready` again. A conflict that is not a clean append → **STOP** and surface it
   to the user.

## 5 — Close-out

Once the train PR is merged (`make land-status` no longer lists the change), a commit
that says `Closes #<n>` has closed its issue — verify `gh issue view <n> --json state` is
`CLOSED`, then `make worktree-rm SLUG=<slug>` from the main working copy. Report which
plans landed, which wave is next, and any plan flagged as stale-vs-`main` (per
`CONTRIBUTING.md`: stop and tell the user rather than guessing).

## Explicit gates (never auto)

- **Review** of every change (§3).
- **Non-append conflicts** during a rebase after a rejection — hand back to the user.
- **`make ready`** — act only on an explicit user decision or a standing approval.
- **Supply-chain files** (`SUPPLY_CHAIN` in `scripts/pr-triage.sh`: CI workflows, installers, the release pipeline, bundled skills, agent-instruction files, the Makefile, `scripts/`) — the user reads those hunks and decides that the change is queued; a standing approval does not cover it.
- **`make worktree-rm`** — it runs `git worktree remove --force`, which discards uncommitted work: confirm the change landed and the slug is the right one first.
