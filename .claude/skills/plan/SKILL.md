---
name: plan
description: Packages the outcome of an analysis/design discussion into a self-contained GitHub plan issue (design → implementation handoff). Use when the user asks to "write the plan" following a discussion, or when a session needs to implement an existing plan issue.
---

# Plan — analysis → implementation handoff

The agent-neutral source of truth is `CONTRIBUTING.md` §Plan issues (procedure
+ self-sufficiency test) and the issue structure in
`.github/ISSUE_TEMPLATE/plan.md`. Read both; this skill only adds the
Claude-side glue.

## Writing a plan

1. Reserve the next free D number (`grep "^## D" docs/decisions.md | tail -3`)
   and use it in the title: `Plan: <title> (D<n>)`. The D entry is written **at
   the end of implementation**, not now: the plan is its draft.
2. Derive the real `file:line` pointers before writing (graphify query /
   targeted symbol grep): the plan contains pointers, not paraphrases.
3. Write the body (in English) to a scratch file following the template
   structure, then:
   `gh issue create --title "Plan: <title> (D<n>)" --label plan --body-file <file>`.
4. Apply the self-sufficiency test from `CONTRIBUTING.md` before submitting.
5. One analysis, several plans → every issue states the cross-plan execution
   order and which sibling plans touch the same files (those land strictly
   sequentially, never in parallel). Amend a plan by editing the issue body
   while nothing is implemented yet; once implementation starts, amend via
   comments only.

## Consuming a plan (implementing session)

1. `gh issue view <n>` (add `--comments`: later amendments live there).
2. Execute the WPs in order, `make vet && make test` after each.
3. Update the docs per the closing checklist, write the `D<n>` entry; the
   implementation PR body includes `Closes #<n>`.
4. Contradiction between plan and code → **stop and flag it** in an issue
   comment: the plan may be stale relative to `main`.
