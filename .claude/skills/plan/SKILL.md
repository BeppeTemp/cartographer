---
name: plan
description: Packages the outcome of an analysis/design discussion into a self-contained operational plan in docs/plans/, ready for a separate implementation session (Sonnet/dev agent). Use when the user asks to "write the plan" following a discussion, or when a session needs to implement an existing plan in docs/plans/.
---

# Plan — analysis → implementation handoff

Repo work pattern: a large model (Fable/Opus) analyzes the problem and decides on solutions in discussion with the user; a separate session (Sonnet, often via the `dev` agent) implements. The plan in `docs/plans/` is the **only bridge** between the two: the implementing session does not see the analysis conversation.

## Rules

- File: `docs/plans/<slug>.md`, committed to main as soon as approved — it is the handoff artifact.
- Reserve the next free D number (`grep "^## D" docs/decisions.md | tail -3`) and use it in the title. The D entry is written **at the end of implementation**, not now: the plan is its draft.
- Language: plans are written in English (open source project); identifiers/API names in English.
- The plan is **consumed**: the implementing session deletes it in the docs closing commit.
- Before writing, derive the real `file:line` pointers (graphify query / targeted symbol grep): the plan contains pointers, not paraphrases of the code.

## Self-sufficiency test (determines the level of detail)

A fresh session with only the plan and the repo must be able to implement without asking questions:

- every non-obvious decision is **already made and justified in one line** — the implementer does not relitigate it;
- no open questions; anything delegated to the implementer is explicitly marked and is only a detail (local naming, test order);
- expected errors and edge cases listed with the desired behavior;
- **no code in the plan**: exact semantics + pointers; implementation is the other session's job.

## File structure

```markdown
# Plan — <title> (D<n>, to implement)

> **Status**: approved, not implemented. To be executed in a dedicated session (Sonnet/`dev` agent).
> On completion: **D<n>** entry in `docs/decisions.md`, update <affected docs>,
> `docs/roadmap.md`, then **delete this file**.

## Context and diagnosis
<the why: evidence, measurements, constraints; decisions already made with their rationale;
invariants to preserve, in bold>

## WP<n> — <title>
<for each WP: one-line goal; file:line to touch; exact semantics and error cases;
tests to add; acceptance criterion. `make vet && make test` green
at the end of each WP.>

## Closing
<checklist: docs to update (docs/index.md §Maintenance rules table), D entry,
roadmap, plan deletion; if a release is planned, semver bump per the deploy skill>
```

## Consumption side (implementing session)

Execute the WPs in order, `make vet && make test` after each WP, update the docs per the banner, delete the plan in the closing commit. If you find a contradiction between the plan and the actual code, **stop and flag it**: the plan may be stale relative to main.
