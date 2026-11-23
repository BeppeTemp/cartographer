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

Plan id for acceptance criteria: <plan id>
Verified against main: <sha>

## Context and diagnosis

The why: evidence, measurements, constraints; decisions already made with their
rationale; invariants to preserve, in bold.

## WP1 — <title>

One WP section per work package: one-line goal; the anchors to touch; exact semantics
and error cases; tests to add; acceptance criteria. `make check` green at the end of
each WP.

- AC-<plan id>-<n> test: <one checkable sentence>
- AC-<plan id>-<n> manual: <one checkable sentence>
- AC-<plan id>-<n> deferred: <one checkable sentence> — <reason>

## Changes

The WPs grouped into the fewest changes the size cap allows (a change is one queued
commit): for each its slug, its commit title, its WPs, the estimated production lines and
the change it is queued after (`AFTER=<slug>`), if any. Each WP is a body line
`type(scope): ...` (one unwrapped line), so the changelog keeps one entry per WP; a change
with one WP omits the body line when it equals the title. A change estimated under about 150
production lines is merged into a neighbouring change of the same plan, unless a code
dependency or a web-bundle conflict forbids it; the changes that depend on each other form
one chain, reviewed and checked once on its tip (`cartographer-implement-plan` §1).

## Closing

- [ ] Docs delta, one line per file and change: `[<change slug>] <file> §<section>: add | change | remove — <what the file says afterwards>`, or `[<change slug>] none — <reason>`; `<file>` is a current-state file (a `docs/` page, `CONTRIBUTING.md`, a skill, an area `AGENTS.md`), and a line without a slug belongs to the plan's last change
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
3. Name **anchors**, never line numbers: a file path plus a symbol in it (a function,
   a type, a make target, a heading, a workflow job, a test name). Lines move when a
   sibling plan lands between the writing and the implementation, so the implementer
   resolves each anchor to `file:line` on its own base. Find them with `Grep`/`Glob`
   rather than by reading whole files, read only the located ranges, and check that
   every anchor exists on `main` and every decision holds there. For an area you do
   not already know, send an `Explore` subagent first and take the anchors from its
   report. The plan contains anchors, not paraphrases, and its
   `Verified against main: <sha>` line names the commit they were checked on.
4. Give the plan its id line and number each WP's acceptance criteria
   (§Plan structure): `AC-<plan id>-<n>`, `<plan id>` matching
   `[A-Za-z0-9]+(-[A-Za-z0-9]+)*` and `<n>` unique in the plan, one checkable sentence
   each, of kind `test` (a test can prove it), `manual` (only a person or a
   release-candidate lane can check it) or `deferred` (with the reason). The criteria
   outlive the plan: the implementer copies them into the change's message. Write the
   Closing's docs delta as what each file says afterwards, each line naming the change
   that makes it (a line without a slug belongs to the plan's last change): the contract reviewer compares a change's
   lines with its diff.
5. Write the plan (in English) to a scratch file outside the repository, following
   the structure above, and hand that file to the maintainer. A plan that answers
   issues names them, so the change can close them.
6. Apply the self-sufficiency test from `CONTRIBUTING.md`, and replace every real KB
   name, host, username, employer or client with a placeholder: the implementer
   pours the plan into a public commit (D259).
7. Group the WPs into the fewest changes the size cap allows, by file-set and dependency
   (`CONTRIBUTING.md` §Plans). A change may build on another: it names it, and is queued
   after it (`AFTER=<slug>`).
8. One analysis, several plans → every plan states the cross-plan execution order
   and which sibling plans touch the same files (those are built one on the other and
   queued in order, never in parallel). A pointer into a sibling's not-yet-implemented artifact cites
   the plan and its WP, never an invented anchor.

## Delegating the drafting

The design decisions are the coordinator's, taken with the user before any draft:
one question per request, with options and a recommendation. Once they are taken,
each plan is drafted by a `cartographer-plan-writer` subagent (`.claude/agents/`,
Sonnet), several in parallel, in the background: the spawn prompt is the issue number
when there is one, the decisions, the sibling plans with their execution order and an
absolute scratch path outside the repository. It reports the decisions the code
contradicts; the coordinator brings those back to the user, then reads the plan
(privacy, execution order) and hands it over per step 5. A request split into several
plans gets one writer per plan.

## Consuming a plan (implementing session)

0. Only a plan file the maintainer gave in the session is implemented, whoever the caller is.

1. Read the file, and resolve every anchor of the change to `file:line`.
2. Execute the WPs in order, `make check` after each.
3. Update the docs per the lines of the Closing's docs delta that name the change (a line without a slug belongs to the plan's last change) and, if it names one, write the pending
   decision file (`make decisions-new SLUG=<slug> TOPIC=<topic>`; never regenerate
   `docs/decisions.md`: the release PR regenerates the index, D328); a change that finishes
   an issue the plan names says `Closes #<n>` in its message, an earlier one `Refs #<n>`;
   the message carries one `AC:` line per criterion of its WPs (`CONTRIBUTING.md` §Plans).
4. Contradiction between plan and code, or an anchor that does not resolve → **stop and tell the maintainer**: the plan
   may be stale relative to `main`.
