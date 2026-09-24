---
name: plan-issue
description: Packages the outcome of an analysis/design discussion into a self-contained GitHub plan issue (design → implementation handoff). Use when the user asks to "write the plan" following a discussion, or when a session needs to implement an existing plan issue.
---

# plan-issue — analysis → implementation handoff

The source of truth is `CONTRIBUTING.md` §Plan issues (procedure and
self-sufficiency test) and the issue structure in
`.github/ISSUE_TEMPLATE/plan.md`. Read both; this skill only adds the
mechanics.

This file is the single copy for every client. `.claude/skills/plan-issue` and
`.kiro/skills/plan-issue` are symlinks to it — see §Per-client mechanics for
the two places where the clients genuinely differ.

## Writing a plan

1. Survey the open plans before designing, not just the code:
   `gh issue list --label plan --state open`, then read the candidates that
   touch the same area. A request already covered by an open plan (or already
   implemented on `main`) is reported back, not re-planned; a partial overlap
   means extending/amending the existing issue or stating the relationship
   (execution order, shared files) in the new one.
2. Reserve the next free D number. Inspect both the implemented records and
   every plan issue title, then choose the next number above both maxima:

   ```bash
   make decisions-next          # highest implemented D number + 1
   gh issue list --label plan --state all --limit 1000
   ```

   An issue title reserves its number even before the entry exists. Use
   `Plan: <title> (D<n>)`; the decision file is written **at the end of
   implementation**, not now — the plan is its draft.
3. Derive the real `file:line` pointers before writing, with your client's
   symbol search rather than by reading whole files, and read only the located
   ranges. For an area you do not already know, send a context-gathering
   subagent first and derive the pointers from its report. The plan contains
   pointers, not paraphrases.
4. Write the body (in English) to a scratch file following the template
   structure, then:
   `gh issue create --title "Plan: <title> (D<n>)" --label plan --body-file <file>`.
   Delete the scratch file afterwards — it is not a repository artifact.
5. Apply the self-sufficiency test from `CONTRIBUTING.md` before submitting, and
   replace every real KB name, host, username, employer or client with a
   placeholder — the issue is public (D259).
6. One analysis, several plans → every issue states the cross-plan execution
   order and which sibling plans touch the same files (those land strictly
   sequentially, never in parallel). Amend a plan by editing the issue body
   while nothing is implemented yet; once implementation starts, amend via
   comments only.
7. Sibling cross-links: create the issues in execution order, then amend each
   body to reference siblings as `D<n> (#<issue>)` — the numbers exist only
   after creation. A pointer into a sibling's not-yet-implemented artifact
   cites the plan (`D<n> WP<m>`), never an invented `file:line`.

## Consuming a plan (implementing session)

1. `gh issue view <n>` (add `--comments`: later amendments live there).
2. Execute the WPs in order, `make gate` after each.
3. Update the docs per the closing checklist and write the decision file
   `docs/decisions/D<n>-<slug>.md` (`make decisions-index` regenerates the
   index); the implementation PR body includes `Closes #<n>`.
4. Contradiction between plan and code → **stop and flag it** in an issue
   comment: the plan may be stale relative to `main`.

## Per-client mechanics

Everything above is identical on every client. These two things are not:

| | How to derive `file:line` pointers | Note |
|---|---|---|
| **Claude Code** | `Grep`/`Glob`, then `Read` on the located ranges; `Explore` subagent for an unknown area | — |
| **Codex** | targeted symbol grep/search | — |
| **Kiro** | the `code` tool (`search_symbols`, `lookup_symbols`, `pattern_search`) or `grep_search`; `context-gatherer` subagent for an unknown area | The skill is `plan-issue`, not `plan`: `/plan` is a built-in Kiro slash command (the planner agent) and the two are unrelated |
| **Antigravity** | codebase search, then read the located ranges | — |
