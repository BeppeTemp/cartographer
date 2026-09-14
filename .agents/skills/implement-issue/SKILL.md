---
name: implement-issue
description: Orchestrate implementation of one or more approved plan issues (label `plan`) into merged PRs — wave planning from cross-plan execution order, delegation to coding subagents in isolated worktrees, coordinator review, and ordered squash-merge. Use when the user asks to implement, ship or land open plan issues (one or many). Sibling of the `plan-issue` skill: `plan-issue` writes issues (design → handoff), `implement-issue` consumes them (issue → merged PR).
---

# implement-issue — plan issues → merged PRs

Source of truth is `CONTRIBUTING.md` §Plan issues + §Pull requests, `AGENTS.md`
(workflow and delegation rules), and `docs/index.md` §Documentation maintenance rules. The
`plan-issue` skill covers writing and consuming a **single** plan. This skill
adds only the **multi-plan orchestration** and the **PR/merge cycle** — read
those first; do not duplicate them here.

This file is the single copy for every client. `.claude/skills/implement-issue`
and `.kiro/skills/implement-issue` are symlinks to it. The three things that
genuinely differ per client are in §Per-client mechanics; everything else in
this file is client-neutral **because the git work lives in `make` targets**,
not in prose.

## Preconditions

- `main` is protected: every plan lands via its own PR, squash-merge, CI green. No direct pushes.
- Merging a self-authored PR and `git push --force-with-lease` require an explicit user decision or a standing approval. **Never work around the approval gate** — surface it and let the user choose.

## 1 — Wave planning (coordinator)

1. Collect the target issues: `gh issue list --label plan` (or the subset the user named). Each title already carries its reserved `D<n>`.
2. `gh issue view <n>` each; extract the **execution order** line (plans state it explicitly) and the **file-set** each touches.
3. Build the graph, two edge types:
   - **Hard code dependency** — a plan uses code a sibling introduces (a new client method, a new helper). These form **strictly sequential chains**: never start a plan before its predecessor is on `main`.
   - **Shared file** — plans touching the same code or the same current-state page can conflict at merge. Decision files no longer share a file: one decision is one `docs/decisions/D<n>-<slug>.md`, so two plans adding two decisions never conflict there. The **generated index** is the exception: it is regenerated, not merged (see §4).
4. Emit **waves**: independent roots with disjoint code file-sets run in parallel; dependency chains run internally sequential but in parallel with each other when their file-sets are disjoint. One plan = one PR.
5. State the wave plan to the user before spawning (spawning N subagents and opening N public PRs is outward-facing).

## 2 — Delegate each plan to a coding subagent

One plan → one coding subagent → one worktree. **No client gives isolation for
free**: every one of them shares the filesystem with its own subagents, so the
coordinator creates the worktree first and puts its absolute path in the
mandate.

```bash
make worktree-add SLUG=<slug>     # fetches origin, branches feat/<slug> from origin/main
```

`.worktrees/` is git-ignored, so a worktree never shows up as untracked noise.
Never run two subagents in the same working copy. Worktrees branch from fresh
`origin/main`, so each subagent sees the merged predecessors — start a chain's
next plan only after the previous PR is merged.

Canonical mandate (self-contained — the subagent never sees this conversation):

- Work **only** inside `<absolute-worktree-path>`; every command runs there. The tools inherit no working directory from this conversation, so pass it explicitly: `git -C <path>`, `make -C <path>`. Sibling subagents are active on other worktrees: never revert or touch their files.
- Read the plan: `gh issue view <n> --comments` — later amendments live in the comments.
- Implement **all** WPs exactly, starting from the `file:line` pointers in the plan; do not re-explore from scratch.
- Write the tests in the plan's "Tests" section.
- **Same session**: update the docs in the "Closing" section per `docs/index.md` §Documentation maintenance rules, and add the decision file `docs/decisions/D<n>-<slug>.md` from `docs/decisions/TEMPLATE.md`. Then `make decisions-index`.
- `make gate` green — iterate until it is.
- Single commit on `feat/<slug>` (the branch already exists in the worktree), message = PR title (conventional commit, the plan gives it), Co-Authored-By trailer.
- `git push -u origin feat/<slug>` + `gh pr create` with a body ending `Closes #<n>`.
- Report `git diff --stat` vs main, the `make gate` outcome, and the PR URL.

## 3 — Review (coordinator GATE — never skip)

For each finished PR: `gh pr diff <pr>` and read it yourself. Trust the disk,
not the subagent's report — report text can be garbled or compressed. Confirm
CI: `gh pr view <pr> --json statusCheckRollup`. A reviewing subagent may be
added on top, but it does **not** replace this read: a self-authored PR without
an independent look defeats two-party review, and this gate is not automatable.

## 4 — Ordered merge

Merge in dependency order. Sibling PRs only conflict when their actual
file-sets overlap:

1. `git fetch origin`, then rebase **in the plan's own worktree**:
   `git -C .worktrees/<slug> rebase origin/main`.
2. A conflict on `docs/decisions/README.md` is **never resolved by hand**: it is
   generated. Take either side, then `make decisions-index` and stage the
   result. Any other conflict that is not a clean append — real code or
   current-state prose divergence — → **STOP** and surface it to the user.
3. `git rebase --continue`; run the plan's affected package tests (`go test ./internal/<pkg>/...`); `git push --force-with-lease`.
4. Wait for CI green and `mergeable == MERGEABLE`, then `gh pr merge <pr> --squash --delete-branch`.
5. `git checkout main && git pull --ff-only` in the main working copy, then
   `make worktree-rm SLUG=<slug>`.

## 5 — Close-out

Each implementation PR closes its issue via `Closes #<n>` — verify
`gh issue view <n> --json state` is `CLOSED`. Report which plans landed, which
wave is next, and any plan flagged as stale-vs-`main` (per `CONTRIBUTING.md`:
stop and comment on the issue rather than guessing).

## Explicit gates (never auto)

- **Review** of every PR diff (§3).
- **Non-append conflicts** during rebase — hand back to the user.
- **Self-merge / force-push** — act only on an explicit user decision or a standing approval.
- **`make worktree-rm`** — it runs `git worktree remove --force`, which discards uncommitted work: confirm the PR is merged and the slug is the right one first.

## Per-client mechanics

| | How to spawn a subagent | Authorization gate |
|---|---|---|
| **Claude Code** | `Task` with `isolation: "worktree"` is available, but pass the `make worktree-add` path explicitly so the layout matches every other client. Default model unless the user asks otherwise | auto-mode classifier; standing allow rules live in `.claude/settings.local.json` (`Bash(gh pr merge *)`, `Bash(git push --force-with-lease *)`) — machine-local, not versioned |
| **Codex** | `collaboration.spawn_agent`, one call per plan. Independent worktrees may run concurrently within the agent-slot limit | explicit user authorization or a standing approval |
| **Kiro** | `orchestrate_subagent`, `role: general-task-execution`, one stage per plan. Independent plans are stages **without** `depends_on` and run in parallel; a dependency chain is `depends_on` on the predecessor | explicit user decision |
| **Antigravity** | one subagent per plan from `.agents/agents/`, or sequential execution if none is defined | `commandExecutionPolicy` on the subagent |
