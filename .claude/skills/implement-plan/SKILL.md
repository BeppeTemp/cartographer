---
name: implement-plan
description: Orchestrate implementation of one or more approved plan issues (label `plan`) into merged PRs — wave planning from cross-plan execution order, delegation to `dev` subagents in isolated worktrees, coordinator review, and ordered squash-merge with docs-append conflict resolution. Use when the user asks to implement/ship/land open plan issues (one or many). Sibling of the `plan` skill: `plan` writes issues (design → handoff), `implement-plan` consumes them (issue → merged PR).
---

# implement-plan — plan issues → merged PRs

Source of truth is `CONTRIBUTING.md` §Plan issues + §Pull requests, `AGENTS.md`/`CLAUDE.md` (workflow, delegation rules), `docs/index.md` §Maintenance rules. The `plan` skill covers writing/consuming a **single** plan. This skill adds only the **multi-plan orchestration** and the **PR/merge cycle** — read those first, don't duplicate them here.

## Preconditions

- **Never push in working hours** (Mon–Fri 09–18): no `git push`/`gh pr create`/`gh pr merge` toward `github.com/BeppeTemp/cartographer` in that window — implement locally, ship outside it. Check `date` first.
- `main` is protected: every plan lands via its own PR, squash-merge, CI `test` green. No direct pushes.
- Merging a self-authored PR and `git push --force-with-lease` are gated by the auto-mode classifier. They require an explicit user decision or allow rules (`Bash(gh pr merge *)`, `Bash(git push --force-with-lease *)` in `.claude/settings.local.json`). **Never work around the gate** — surface it and let the user choose.

## 1 — Wave planning (coordinator)

1. Collect the target issues: `gh issue list --label plan` (or the subset the user named). Each title already carries its reserved `D<n>`.
2. `gh issue view <n>` each; extract the **execution order** line (plans state it explicitly) and the **file-set** each touches.
3. Build the graph, two edge types:
   - **Hard code dependency** — a plan uses code a sibling introduces (e.g. a new client method, a new helper). These form **strictly sequential chains**: never start a plan before its predecessor is on `main`.
   - **Shared file** — plans touching the same file conflict at merge (always true for `docs/decisions.md`/`docs/index.md`: each appends its own `D<n>`). Not a start-order constraint, a merge-order one.
4. Emit **waves**: independent roots with disjoint code file-sets run in parallel; dependency chains run internally sequential but in parallel with each other when their file-sets are disjoint. One plan = one PR.
5. State the wave plan to the user before spawning (spawning N dev agents + opening N public PRs is outward-facing).

## 2 — Delegate each plan to a `dev` subagent

One plan → one `dev` (Sonnet default; `model: opus` only on explicit user request for hard algorithm/architecture/subtle-debug work), `isolation: "worktree"`, `run_in_background: true`. Never two agents on the same working copy. Worktrees branch from `origin/main` (fresh), so each dev sees the merged predecessors — only start a chain's next plan after the previous PR is merged.

Canonical mandate (self-contained — the dev never sees this conversation):

- Read the plan: `gh issue view <n>` (add `--comments` — later amendments live there).
- Implement **all** WPs exactly, starting from the `file:line` pointers in the plan (don't re-explore from scratch).
- Write the tests in the plan's "Tests" section.
- **Same session**: update the docs in the "Closing" section per `docs/index.md` §Maintenance rules, add the `## D<n>` entry to `docs/decisions.md` (grep `## D<n-1>` for position/format).
- `make vet && make test` green — iterate until they are.
- Branch `feat/<slug>` (from the plan), single commit, message = PR title (conventional commit, the plan gives it), Co-Authored-By trailer.
- `git push -u origin feat/<slug>` + `gh pr create` with body ending `Closes #<n>` and the Generated-with trailer.
- Report `git diff --stat` vs main, `make vet && make test` outcome, PR URL.

## 3 — Review (coordinator GATE — never skip)

For each finished PR: `gh pr diff <pr>` and read it. Trust the disk, not the dev's report (report text can be garbled/compressed). Confirm CI: `gh pr view <pr> --json statusCheckRollup`. This gate is not automatable — a self-authored PR without an independent look defeats two-party review.

## 4 — Ordered merge with append-conflict resolution

Merge in dependency order. After each merge the sibling PRs sharing docs go `CONFLICTING` — that is **expected and systematic** (append of distinct `D<n>` entries at the same offset), not a real conflict:

1. `git fetch origin`; `git checkout -B feat/<slug> origin/feat/<slug>`; `git rebase origin/main`.
2. Resolve the append conflict: keep **both** entries in **numeric order** in `docs/decisions.md`; keep both rows in `docs/index.md` §Maintenance rules. A conflict that is **not** a clean append (real code/prose divergence) → **STOP**, surface to the user.
3. `git rebase --continue`; run the plan's affected package tests (`go test ./internal/<pkg>/...`); `git push --force-with-lease`.
4. Wait for CI `test` = SUCCESS and `mergeable == MERGEABLE`, then `gh pr merge <pr> --squash --delete-branch`.
5. `git checkout main && git pull --ff-only`. Cleanup the dev's worktree: `git worktree remove --force .claude/worktrees/agent-<id>`, delete stale local `feat/*` + `worktree-agent-*` branches.

## 5 — Close-out

Each implementation PR closes its issue via `Closes #<n>` — verify `gh issue view <n> --json state` is `CLOSED`. Report which plans landed, which wave is next, and any plan you flagged as stale-vs-`main` (per `CONTRIBUTING.md`: stop and comment on the issue rather than guessing).

## Explicit gates (never auto)

- **Review** of every PR diff (§3).
- **Non-append conflicts** during rebase — hand back to the user.
- **Self-merge / force-push** — respect the classifier; act only on an explicit user decision or a standing allow rule.
