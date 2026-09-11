---
name: implement-issue
description: Orchestrate implementation of one or more approved plan issues (label `plan`) into merged PRs — wave planning from cross-plan execution order, delegation to coding subagents in dedicated worktrees, coordinator review, and ordered squash-merge with topic-owned documentation conflict resolution. Use when the user asks to implement/ship/land open plan issues (one or many). Sibling of the `plan-issue` skill: `plan-issue` writes issues (design → handoff), `implement-issue` consumes them (issue → merged PR).
---

# implement-issue — plan issues → merged PRs

Source of truth is `CONTRIBUTING.md` §Plan issues + §Pull requests,
`AGENTS.md`/`CLAUDE.md` (workflow, delegation rules), `docs/index.md`
§Maintenance rules. The `plan` skill covers writing/consuming a **single**
plan. This skill adds only the **multi-plan orchestration** and the **PR/merge
cycle** — read those first, don't duplicate them here.

## Preconditions

- `main` is protected: every plan lands via its own PR, squash-merge, CI `test` green. No direct pushes.
- Merging a self-authored PR and `git push --force-with-lease` require an explicit user decision. **Never work around the approval gate** — surface it and let the user choose.

## 1 — Wave planning (coordinator)

1. Collect the target issues: `gh issue list --label plan` (or the subset the user named). Each title already carries its reserved `D<n>`.
2. `gh issue view <n>` each; extract the **execution order** line (plans state it explicitly) and the **file-set** each touches.
3. Build the graph, two edge types:
   - **Hard code dependency** — a plan uses code a sibling introduces (e.g. a new client method, a new helper). These form **strictly sequential chains**: never start a plan before its predecessor is on `main`.
   - **Shared file** — plans touching the same code, current-state page or
     decision topic can conflict at merge. A D entry only touches its owning
     `docs/decisions/<topic>.md`; unrelated topics are not a shared file.
4. Emit **waves**: independent roots with disjoint code file-sets run in parallel; dependency chains run internally sequential but in parallel with each other when their file-sets are disjoint. One plan = one PR.
5. State the wave plan to the user before spawning (spawning N subagents + opening N public PRs is outward-facing).

## 2 — Delegate each plan to a coding subagent

**Kiro subagents share the filesystem: no isolation comes for free.** Create the
worktree yourself before spawning, and put its absolute path in the mandate:

```bash
git fetch origin
git worktree add -b feat/<slug> .kiro/worktrees/<slug> origin/main
```

`.kiro/worktrees/` is git-ignored, so a worktree never shows up as untracked
noise. Never run two subagents in the same working copy. Worktrees branch from
fresh `origin/main`, so each subagent sees the merged predecessors — only start
a chain's next plan after the previous PR is merged.

Spawn with `orchestrate_subagent`, `role: general-task-execution`, one stage per
plan. Independent plans are stages **without** `depends_on` and run in parallel;
a dependency chain is expressed with `depends_on` on the predecessor stage.

Canonical mandate (self-contained — the subagent never sees this conversation):

- Work **only** inside `<absolute-worktree-path>`; every command runs there (the tools inherit no cwd from this conversation, so pass it explicitly: `git -C <path>`, `make -C <path>`). Sibling subagents are active on other worktrees: never revert or touch their files.
- Read the plan: `gh issue view <n>` (add `--comments` — later amendments live there).
- Implement **all** WPs exactly, starting from the `file:line` pointers in the plan (don't re-explore from scratch).
- Write the tests in the plan's "Tests" section.
- **Same session**: update the docs in the "Closing" section per `docs/index.md` §Maintenance rules, add the `## D<n>` entry to the owning topic named by the plan (search that file for the local format; keep entries numerically ordered).
- `make vet && make test` green — iterate until they are.
- Single commit on `feat/<slug>` (the branch already exists in the worktree), message = PR title (conventional commit, the plan gives it), Co-Authored-By trailer.
- `git push -u origin feat/<slug>` + `gh pr create` with body ending `Closes #<n>` and the Generated-with trailer.
- Report `git diff --stat` vs main, `make vet && make test` outcome, PR URL.

## 3 — Review (coordinator GATE — never skip)

For each finished PR: `gh pr diff <pr>` and read it yourself. Trust the disk,
not the subagent's report (report text can be garbled/compressed). Confirm CI:
`gh pr view <pr> --json statusCheckRollup`. A `semantic_reviewer` subagent may
be added on top, but it does **not** replace this read: a self-authored PR
without an independent look defeats two-party review, and this gate is not
automatable.

## 4 — Ordered merge with documentation-conflict resolution

Merge in dependency order. Sibling PRs only conflict when their actual
file-sets overlap; decision entries in different topic files do not:

1. `git fetch origin`; run the rebase **in the plan's own worktree**: `git -C .kiro/worktrees/<slug> rebase origin/main`.
2. Resolve a clean decision append by keeping **both** entries in numeric order
   in the shared topic file. A conflict that is not a clean append (real
   code/current-state prose divergence) → **STOP**, surface to the user.
3. `git rebase --continue`; run the plan's affected package tests (`go test ./internal/<pkg>/...`); `git push --force-with-lease`.
4. Wait for CI `test` = SUCCESS and `mergeable == MERGEABLE`, then `gh pr merge <pr> --squash --delete-branch`.
5. `git checkout main && git pull --ff-only` in the main working copy. Cleanup: `git worktree remove --force .kiro/worktrees/<slug>`, then delete the stale local `feat/<slug>` branch after verifying the exact name.

## 5 — Close-out

Each implementation PR closes its issue via `Closes #<n>` — verify `gh issue view <n> --json state` is `CLOSED`. Report which plans landed, which wave is next, and any plan you flagged as stale-vs-`main` (per `CONTRIBUTING.md`: stop and comment on the issue rather than guessing).

## Explicit gates (never auto)

- **Review** of every PR diff (§3).
- **Non-append conflicts** during rebase — hand back to the user.
- **Self-merge / force-push** — act only on an explicit user decision.
- **Worktree removal** — `--force` discards uncommitted work: confirm the PR is merged and the path is the right one before running it.
