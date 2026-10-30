---
name: cartographer-propose-plan
description: Use when the user asks to "write the plan", to hand a design discussion over to an implementer, or to promote a bug to a plan — packages the outcome of an analysis into a self-contained GitHub plan issue (design → implementation handoff). Implementing a plan is `cartographer-implement-plan`.
---

# cartographer-propose-plan — analysis → implementation handoff

The source of truth is `CONTRIBUTING.md` §Plan issues (procedure and
self-sufficiency test) and the issue structure in
`.github/ISSUE_TEMPLATE/plan.md`. Read both; this skill only adds the
mechanics.

## Role

Before anything else, find your role (D955; `CONTRIBUTING.md` §Working with an agent client):

```bash
gh repo view "$(scripts/upstream-repo.sh)" --json viewerPermission --jq .viewerPermission
```

`ADMIN`, `MAINTAIN` or `WRITE` is **maintainer mode**; `TRIAGE`, `READ`, or any
failure to read it, is **contributor mode**. State the mode to the user in one line.

A plan is a **proposal**: the skill ends by telling the user it waits for a
maintainer's `approved` label, and never adds that label itself (a maintainer may
tell you to, in the session). In contributor mode the label edits below are out of
reach (they need triage rights): you create a Plan issue, nothing else. To
implement an approved plan, a contributor comments on it to claim it; the
maintainer assigns it to them.

## Writing a plan

1. Survey the open plans before designing, not just the code:
   `gh issue list --label plan --state open`, then read the candidates that
   touch the same area. A request already covered by an open plan (or already
   implemented on `main`) is reported back, not re-planned; a partial overlap
   means extending/amending the existing issue or stating the relationship
   (execution order, shared files) in the new one.
2. Nothing to reserve: the plan's decision number is its issue number (D672).
   Title it `Plan: <title>`. Whether it needs a decision file at all (an
   architectural or contract choice) is stated in its Closing section; the file
   is written **at the end of implementation**, not now — the plan is its draft.
3. Derive the real `file:line` pointers before writing, with `Grep`/`Glob`
   rather than by reading whole files, and read only the located
   ranges. For an area you do not already know, send an `Explore`
   subagent first and derive the pointers from its report. The plan contains
   pointers, not paraphrases.
4. Write the body (in English) to a scratch file following the template
   structure, then:
   `gh issue create --title "Plan: <title>" --label plan --body-file <file>` (a contributor cannot set labels: the template sets `plan`).
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
   body to reference siblings as `#<issue>` — the numbers exist only after
   creation. A pointer into a sibling's not-yet-implemented artifact cites the
   plan (`#<issue> WP<m>`), never an invented `file:line`.
8. A plan written from a `bug` or `idea` issue is **promoted in place**, not filed anew
   (maintainer mode): rewrite that issue's body from the template (keep a
   one-line summary of the original request at the top), retitle it
   `Plan: <title>`, and `gh issue edit <m> --add-label plan`. One issue, one
   history, nothing to close by hand. In contributor mode, open a new Plan issue
   that links the original instead. Only an analysis that splits one request
   into several plans files new issues; it then closes the request with
   `-c "Split into #<a>, #<b>"`.

## Delegating the drafting

The design decisions are the coordinator's, taken with the user before any draft:
one question per request, with options and a recommendation. Once they are taken,
each plan body is drafted by a `cartographer-plan-writer` subagent (`.claude/agents/`,
Sonnet), several in parallel, in the background: the spawn prompt is the issue number,
the decisions, the sibling plans with their execution order and an absolute scratch
path outside the repository. It reports the decisions the code contradicts; the
coordinator brings those back to the user, then reads the body (privacy, execution
order) and publishes it per steps 4-8 above. A request split into several plans gets
one writer per plan.

## Consuming a plan (implementing session)

0. Only a plan labelled `approved` is implemented, whoever the caller is: check `gh issue view <n> --json labels` first and stop if it is missing.

1. `gh issue view <n> --json body,comments --jq '.body, (.comments[].body)'` (body, then the comments where later amendments live).
2. Execute the WPs in order, `make gate` after each.
3. Update the docs per the closing checklist and, if it names one, write the
   decision file `docs/decisions/D<issue>-<slug>.md` (never regenerate `docs/decisions.md` in the
   PR: the release PR regenerates the index (D328)); the implementation PR body includes `Closes #<n>`.
4. Contradiction between plan and code → **stop and flag it** in an issue
   comment: the plan may be stale relative to `main`.
