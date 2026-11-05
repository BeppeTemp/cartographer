---
name: cartographer-propose-plan
description: Use when the user asks to "write the plan", to hand a design discussion over to an implementer, or to turn a bug or an idea into a plan — packages the outcome of an analysis into a self-contained plan file (design → implementation handoff). Implementing a plan is `cartographer-implement-plan`.
---

# cartographer-propose-plan — analysis → implementation handoff

The source of truth is `CONTRIBUTING.md` §Plans (procedure and self-sufficiency
test). Read it; this skill only adds the mechanics and the plan's structure.

## Role

Before anything else, find your role (D955; `CONTRIBUTING.md` §Working with an agent client):

```bash
gh repo view "$(scripts/upstream-repo.sh)" --json viewerPermission --jq .viewerPermission
```

`ADMIN`, `MAINTAIN` or `WRITE` is **maintainer mode**; `TRIAGE`, `READ`, or any
failure to read it, is **contributor mode**. State the mode to the user in one line.

In contributor mode this skill stops here: a contributor opens a bug, an idea or a
direct PR, and writes no plan; tell the user so.

A plan is a **proposal** until the maintainer gives it to an implementing session:
the skill ends by handing the file to the maintainer, who keeps it outside the
repository.

## Plan structure

```markdown
# <title>

## Context and diagnosis

The why: evidence, measurements, constraints; decisions already made with their
rationale; invariants to preserve, in bold.

## WP1 — <title>

One WP section per work package: one-line goal; file:line to touch; exact semantics
and error cases; tests to add; acceptance criterion. `make check` green at the end of
each WP.

## Changes

The WPs grouped into the fewest changes the size cap allows (a change is one queued
commit): for each its slug, its commit title, its WPs, the estimated production lines and
the change it is queued after (`AFTER=<slug>`), if any. Each WP is a body line
`type(scope): ...` (one unwrapped line), so the changelog keeps one entry per WP; a change
with one WP omits the body line when it equals the title.

## Closing

- [ ] Current-state docs: `docs/<page>.md`
- [ ] Decision file: `docs/decisions/D-new-<slug>.md` (numbered when it lands), `topic: <topic>` (one of `repodocs.TopicOrder`; `make decisions-new` lists them), or `none` (no architectural or contract choice)
- [ ] Traps: fixed with a test or a comment next to the code, or `none`
- [ ] Release impact: `<none | describe>`
```

## Writing a plan

1. Survey what the maintainer gives you as planned work, and the code, before
   designing. A request already covered or already implemented on `main` is reported
   back, not re-planned; a partial overlap means stating the relationship (execution
   order, shared files) in the new plan.
2. Whether the plan needs a decision file at all (an architectural or contract
   choice) is stated in its Closing section; the file is written **at the end of
   implementation**, not now — the plan is its draft. It is pending, `D-new-<slug>`, and
   the lander numbers it (D1706).
3. Derive the real `file:line` pointers before writing, with `Grep`/`Glob`
   rather than by reading whole files, and read only the located
   ranges. For an area you do not already know, send an `Explore`
   subagent first and derive the pointers from its report. The plan contains
   pointers, not paraphrases.
4. Write the plan (in English) to a scratch file outside the repository, following
   the structure above, and hand that file to the maintainer. A plan that answers
   issues names them, so the change can close them.
5. Apply the self-sufficiency test from `CONTRIBUTING.md`, and replace every real KB
   name, host, username, employer or client with a placeholder: the implementer
   pours the plan into a public commit (D259).
6. Group the WPs into the fewest changes the size cap allows, by file-set and dependency
   (`CONTRIBUTING.md` §Plans). A change may build on another: it names it, and is queued
   after it (`AFTER=<slug>`).
7. One analysis, several plans → every plan states the cross-plan execution order
   and which sibling plans touch the same files (those are built one on the other and
   queued in order, never in parallel). A pointer into a sibling's not-yet-implemented artifact cites
   the plan and its WP, never an invented `file:line`.

## Delegating the drafting

The design decisions are the coordinator's, taken with the user before any draft:
one question per request, with options and a recommendation. Once they are taken,
each plan is drafted by a `cartographer-plan-writer` subagent (`.claude/agents/`,
Sonnet), several in parallel, in the background: the spawn prompt is the issue number
when there is one, the decisions, the sibling plans with their execution order and an
absolute scratch path outside the repository. It reports the decisions the code
contradicts; the coordinator brings those back to the user, then reads the plan
(privacy, execution order) and hands it over per step 4. A request split into several
plans gets one writer per plan.

## Consuming a plan (implementing session)

0. Only a plan file the maintainer gave in the session is implemented, whoever the caller is.

1. Read the file.
2. Execute the WPs in order, `make check` after each.
3. Update the docs per the closing checklist and, if it names one, write the pending
   decision file (`make decisions-new SLUG=<slug> TOPIC=<topic>`; never regenerate
   `docs/decisions.md`: the release PR regenerates the index, D328); a change that finishes
   an issue the plan names says `Closes #<n>` in its message, an earlier one `Refs #<n>`.
4. Contradiction between plan and code → **stop and tell the maintainer**: the plan
   may be stale relative to `main`.
