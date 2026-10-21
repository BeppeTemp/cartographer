---
name: cartographer-implement-plan
description: Use when the user asks to implement, ship or land one or more approved plan issues (labels `plan` and `approved`) — plans the waves from the cross-plan execution order, delegates each plan to a coding subagent in an isolated worktree, reviews, and squash-merges in order. Sibling of `cartographer-propose-plan`, which writes the issues this skill consumes (issue → merged PR).
---

# cartographer-implement-plan — plan issues → merged PRs

Source of truth is `CONTRIBUTING.md` §Plan issues + §Pull requests, `AGENTS.md`
(workflow and delegation rules), and `docs/index.md` §Documentation maintenance rules. The
`cartographer-propose-plan` skill covers writing and consuming a **single** plan. This skill
adds only the **multi-plan orchestration** and the **PR/merge cycle** — read
those first; do not duplicate them here.

This file is the single copy for every client. `.claude/skills/cartographer-implement-plan`,
`.kiro/skills/cartographer-implement-plan` and `.opencode/skills/cartographer-implement-plan` are symlinks to it. The three things that
genuinely differ per client are in §Per-client mechanics; everything else in
this file is client-neutral **because the git work lives in `make` targets**,
not in prose.

## Role

Before anything else, find your role (D955; `CONTRIBUTING.md` §Working with an agent client):

```bash
gh repo view "$(scripts/upstream-repo.sh)" --json viewerPermission --jq .viewerPermission
```

`ADMIN`, `MAINTAIN` or `WRITE` is **maintainer mode**; `TRIAGE`, `READ`, or any
failure to read it, is **contributor mode**. State the mode to the user in one line.

- **Only an approved plan is implemented**, in either mode, the maintainer's own
  plans included: select with `--label plan --label approved`. A plan the user
  names that lacks `approved` is not implemented: say it awaits a maintainer's
  approval. In contributor mode the plan must also be assigned to you (comment
  on it to claim it; the maintainer assigns it): `plan-gate` rejects the PR
  otherwise. Never add the label on your own initiative, only when the maintainer
  tells you to in this session.
- **Contributor mode**: exactly one plan, no waves (§1 is skipped); the branch
  is pushed to `origin`, your fork. No `gh pr merge`, no armed auto-merge (§4
  step 4 is skipped), no force-push you were not asked for. The skill ends with
  the PR open and its `## Self-review` section filled.
- **Maintainer mode**: unchanged from here on.

## Preconditions

- `main` is protected: every plan lands via its own PR, squash-merge, CI green. No direct pushes.
- Merging a self-authored PR and `git push --force-with-lease` require an explicit user decision or a standing approval. **Never work around the approval gate** — surface it and let the user choose.

## 0 — Keep the coordinator cheap

A batch of plans is easily 300 KB of issue text; the coordinator must not read it.
The coordinator's context holds only the wave graph, the diffs and the merge
state; the plans are read by the subagents that implement them.

- **Ask once, up front**, in a single question: the wave plan, plus a standing
  approval for self-merge (squash on green CI after the §3 read) and
  `--force-with-lease` on `feat/*` for the session. Then run without
  per-PR check-ins, stopping only at the §Explicit gates that remain.
- **Never `gh issue view` a whole plan** for wave planning: extract only what the
  graph needs (step 2 below).
- **Spawn prompt = issue number + absolute worktree path** (plus the sibling
  plans in flight); the `cartographer-plan-implementer` agent carries the rest (D327).
- **Implementers run on a cheaper model** than the coordinator, pinned in each
  agent definition (Claude Code `claude-sonnet-5-5`, Kiro and Copilot CLI
  `claude-sonnet-5.5`, OpenCode `anthropic/claude-sonnet-5-5`): the plan already
  carries the design, so the implementer executes, and the coordinator's §3 read
  is the quality gate.
- **The code is read by the tier's reviewers, not by the coordinator** (§3): the
  coordinator reads the triage, the findings it has verified and the hunks §3
  names — never the whole diff of a standard or full tier PR.
- **Run everything in the background** and act on completion notifications:
  never poll or read a subagent transcript.

## 1 — Wave planning (coordinator)

1. Collect the target issues: `gh issue list --label plan --label approved` (or the subset the user named, if it carries both labels). A plan's decision number, when it has one, is its issue number (D672).
2. Extract, per plan, only the **execution order** paragraph and the **file-set**, from one bulk fetch:

   ```bash
   plans=$(mktemp)   # outside the working copy: nothing here is git-ignored
   gh issue list --label plan --label approved --state open --limit 100 --json number,body > "$plans"
   for n in $(jq -r '.[].number' "$plans" | sort -n); do
     b=$(jq -r ".[]|select(.number==$n).body" "$plans"); echo "=== #$n"
     echo "$b" | grep -iA5 -m2 'execution order\|cross-plan'
     echo "FILES: $(echo "$b" | grep -oE '(internal|cmd|docs|web|scripts|\.github|\.agents)/[A-Za-z0-9_./-]+\.(go|md|ts|tsx|sh|yml)' | grep -v decisions/ | sort -u | tr '\n' ' ')"
   done
   ```

   Plans written in the same batch often **contradict each other** on the order
   of plans that only share files (A says "before B", B says "before A"). That is
   not a hard dependency: pick one order and state it in the wave plan.
3. Build the graph, two edge types:
   - **Hard code dependency** — a plan uses code a sibling introduces (a new client method, a new helper). These form **strictly sequential chains**: never start a plan before its predecessor is on `main`.
   - **Shared file** — plans touching the same code or the same current-state page can conflict at merge. Decision files no longer share a file: one decision is one `docs/decisions/D<n>-<slug>.md`, so two plans adding two decisions never conflict there. The **generated index** is the exception: it is regenerated, not merged (see §4).
4. Plans that share a **hot file** (one touched by three or more plans, e.g. `internal/lint/lint.go` or `internal/mcpserver/conformance.go`) go in **one sequential chain**, not in parallel waves: each rebase there is a real conflict, and resolving it costs more than the time saved.
5. Emit **waves**: independent roots with disjoint code file-sets run in parallel; dependency chains run internally sequential but in parallel with each other when their file-sets are disjoint. One plan = one PR.
6. State the wave plan to the user before spawning (spawning N subagents and opening N public PRs is outward-facing).

## 2 — Delegate each plan to a coding subagent

One plan → one coding subagent → one worktree. **No client gives isolation for
free**: every one of them shares the filesystem with its own subagents, so the
coordinator creates the worktree first and puts its absolute path in the
mandate.

```bash
make worktree-add SLUG=<slug>     # fetches upstream (origin when there is no upstream remote), branches feat/<slug> from its main
```

`.worktrees/` is git-ignored, so a worktree never shows up as untracked noise.
Never run two subagents in the same working copy. Worktrees branch from fresh
`upstream/main` (`origin/main` without an `upstream` remote), so each subagent sees the merged predecessors — start a chain's
next plan only after the previous PR is merged.

Spawn the `cartographer-plan-implementer` subagent (D884: `.claude/agents/`, `.kiro/agents/`,
`.opencode/agents/`, `.github/agents/`, same text): it already carries the mandate below and a fixed
≤10-line report, so the spawn prompt is just the issue number and the absolute
worktree path. Where the client has no such agent, pass the mandate inline.

Canonical mandate (self-contained — the subagent never sees this conversation):

- Work **only** inside `<absolute-worktree-path>`; every command runs there. The tools inherit no working directory from this conversation, so pass it explicitly: `git -C <path>`, `make -C <path>`. Sibling subagents are active on other worktrees: never revert or touch their files.
- First check the plan carries `approved` (`gh issue view <n> --json labels`); without it stop, nothing is implemented.
- Read the plan: `gh issue view <n> --json body,comments --jq '.body, (.comments[].body)'` — later amendments live in the comments. Not `--comments`: without a TTY (every subagent) it prints the comments only and drops the body.
- Implement **all** WPs exactly, starting from the `file:line` pointers in the plan; do not re-explore from scratch.
- Write the tests in the plan's "Tests" section.
- **Same session**: update the docs in the "Closing" section per `docs/index.md` §Documentation maintenance rules, and, if the Closing section names one, add the decision file `docs/decisions/D<issue>-<slug>.md` (`make decisions-new`); never regenerate `docs/decisions.md` (the release PR does, D328). Every trap you hit is fixed where it bites: a test if it can be checked, a comment next to the code otherwise (D214).
- `make gate` green — iterate until it is.
- One commit for the implementation on `feat/<slug>` (the branch already exists in the worktree), message = PR title (conventional commit, the plan gives it), Co-Authored-By trailer. A review fix round **adds commits, it never amends**: the next round diffs `<reviewed commit>..HEAD`, and an amend rewrites the commit it names.
- `git push -u origin feat/<slug>` + `gh pr create` with a body ending `Closes #<n>`.
  The PR body, commit messages and any test fixture carry placeholder names only —
  never a KB, host, user or project from a real setup (D259, `AGENTS.md`).
  Leave the `## Self-review` section as "pending review": the coordinator writes
  it after the last round (§3).
- Report `git diff --stat` vs main, the `make gate` outcome, and the PR URL.

## 3 — Review (coordinator GATE — never skip)

For each finished PR, `.agents/skills/cartographer-review-pr/scripts/triage.sh <pr>` gives the
tier; the procedure is `cartographer-review-pr` §3–§5, with the coordinator on the verifying
side and the implementer on the author side. Trust the disk, not the
subagent's report — report text can be garbled or compressed.

- **Light tier**: the coordinator reads `gh pr diff <pr>` itself; one round.
- **Standard and full tier**: spawn the reviewers of the tier (`cartographer-review-pr` §3:
  `cartographer-contract-reviewer`, plus `cartographer-security-reviewer` on full) on `.worktrees/<slug>` (round 1,
  plan = the issue number), verify its findings per `cartographer-review-pr` §4, and send the
  **verified** findings — never the raw report — to the implementer that wrote
  the PR (§Per-client mechanics). The fix mandate: add commits on the branch,
  never amend; answer each finding; one report line per finding. Repeat per
  `cartographer-review-pr` §5 (at most three rounds, later rounds read `<reviewed commit>..HEAD`).
  The pre-merge rebase (§4) comes after the last round.
- **What the coordinator reads**: the triage, the verified findings, the ranges of
  the reviewer's invariants line (full tier), and supply-chain hunks whole.
- After the last round the coordinator writes the PR's `## Self-review` section
  (`gh pr edit`): rounds run, each finding with its answer, `pre-existing`
  findings and the `bug` issues opened for them.
- Confirm CI: `gh pr view <pr> --json statusCheckRollup`. When a check failed,
  spawn `cartographer-source-reader` (a read-only subagent on a cheaper model) with the PR number and
  the question "why is CI red": it returns the failed jobs, test names and first
  error lines. Send the implementer that digest in the fix mandate, never the raw
  log.

## 4 — Ordered merge

Maintainer mode only: in contributor mode the skill ended at the open PR (§3).

Merge in dependency order. Sibling PRs only conflict when their actual
file-sets overlap:

1. `git fetch <upstream>` (the `upstream` remote when it exists, else `origin`), then rebase **in the plan's own worktree**:
   `git -C .worktrees/<slug> rebase <upstream>/main`.
2. A conflict inside the **generated block** of `docs/decisions.md` (an older
   PR that still regenerated it) is never resolved by hand: take `origin/main`'s
   side — the release PR regenerates it (D328). The same for
   `internal/webui/dist/`: take `origin/main`'s side, then `make web`. Any other conflict that is not a
   clean append — real code or current-state prose divergence — → **STOP** and
   surface it to the user.
3. `git rebase --continue`, then the **full** `make -C .worktrees/<slug> gate` on the rebased tree, then `git push --force-with-lease`. Rebase onto the `main` that already holds the previous merges even when GitHub says the PR is mergeable: two PRs that each pass alone can fail together — a whole-catalogue budget (D285), a duplicate decision number — and only the combined tree shows it. The local gate (~2 min) catches that before it breaks `main` (D328).
4. **Next train** (CONTRIBUTING.md §Releases): a plan in a later milestone than the open one due first is implemented and reviewed, but not merged: its PR waits, open, and `plan-gate` stays red until the train before it has left. Say so, and pick it up after Monday's release. **Freeze** (D956): from Friday 00:00 Europe/Rome until Monday's train has run (`TZ=Europe/Rome date +%u%H%M`: weekday 5-7, or Monday before 0830; on Monday after that, until `gh run list --workflow release-train.yml --event schedule --created "$(date -u +%F)" --json status` shows two runs, both `completed`: the cron often starts late, and a merge before the train skips it) a PR whose title type is `feat`, `perf` or `refactor`, or that is breaking (`!:` or a `BREAKING CHANGE` footer), is not merged and gets no auto-merge: say it waits until after Monday's release. Other types are unaffected. Otherwise `gh pr merge <pr> --squash --delete-branch --auto` right after the push: GitHub merges it when the required checks pass (D329), with no wait loop on this side. Check `gh pr view <pr> --json state` before the next plan's rebase; a check that fails leaves the PR open (read it through `cartographer-source-reader`, as in §3), and auto-merge is cancelled by any later push.
5. `git checkout main && git pull --ff-only` in the main working copy, then
   `make worktree-rm SLUG=<slug>`.

## 5 — Close-out

Each implementation PR closes its issue via `Closes #<n>` — verify
`gh issue view <n> --json state` is `CLOSED`. Report which plans landed, which
wave is next, and any plan flagged as stale-vs-`main` (per `CONTRIBUTING.md`:
stop and comment on the issue rather than guessing).

## Explicit gates (never auto)

- **Review** of every PR (§3).
- **Non-append conflicts** during rebase — hand back to the user.
- **Self-merge / force-push** — act only on an explicit user decision or a standing approval.
- **Supply-chain files** (a CI workflow, an installer, the release pipeline, a bundled skill: `triage.sh` says so) — the user reads those hunks and decides that merge; a standing approval does not cover it.
- **`make worktree-rm`** — it runs `git worktree remove --force`, which discards uncommitted work: confirm the PR is merged and the slug is the right one first.

## Per-client mechanics

| | How to spawn a subagent | Fix round (§3) | Authorization gate |
|---|---|---|---|
| **Claude Code** | `Agent` with `subagent_type: cartographer-plan-implementer`; `isolation: "worktree"` is available, but pass the `make worktree-add` path explicitly so the layout matches every other client. The agent pins Sonnet 5.5 (`model:` in its frontmatter) | `SendMessage` to the implementer's agent id with the verified findings |  auto-mode classifier; a standing approval is the user's own Claude Code permission settings (`Bash(gh pr merge *)`, `Bash(git push --force-with-lease *)`) |
| **Kiro** | `orchestrate_subagent` with the `cartographer-plan-implementer` agent (or `role: general-task-execution`), one stage per plan. Independent plans are stages **without** `depends_on` and run in parallel; a dependency chain is `depends_on` on the predecessor | a new `cartographer-plan-implementer` with the fix mandate (worktree path, PR number, the verified findings) |  explicit user decision or a standing approval |
| **OpenCode** | the `subagent` tool with `agent: cartographer-plan-implementer` (`background: true` to run it in the background), one call per plan; `explore` is the built-in context-gathering subagent | a new `cartographer-plan-implementer` with the fix mandate (worktree path, PR number, the verified findings) |  explicit user decision or a standing approval |
| **Copilot CLI** | the `task` tool with `agent_type: cartographer-plan-implementer`, one call per plan; the agent comes from `.github/agents`, which loads once the folder is trusted. If the session cannot delegate to it, pass the body of `.claude/agents/cartographer-plan-implementer.md` inline as the mandate | a new `cartographer-plan-implementer` with the fix mandate (worktree path, PR number, the verified findings) |  explicit user decision or a standing approval |
| **No subagents** (Codex, Antigravity, Crush: they read `.agents/skills` but have no repository agents) | Pass the canonical mandate (§2) inline to a fresh session or a general-purpose subagent, one per plan, with the issue number and the absolute worktree path | the same mandate plus the verified findings | explicit user decision or a standing approval |
