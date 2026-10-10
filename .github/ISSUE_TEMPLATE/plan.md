---
name: Plan
about: Self-contained implementation plan (design → implementation handoff)
title: 'Plan: <title>'
labels: plan
---

> **Status**: approved, not implemented. Its decision number is this issue's
> number (D673). On completion: update the affected current-state docs
> (`docs/index.md` §Documentation maintenance rules), add
> `docs/decisions/D<issue>-<slug>.md` only if the plan makes an architectural or
> contract choice, then close this issue from the implementation PR
> (`Closes #<issue>`).

## Context and diagnosis

<!-- The why: evidence, measurements, constraints; decisions already made with
their rationale; invariants to preserve, in bold. -->

## WP1 — <title>

<!-- One WP section per work package: one-line goal; file:line to touch; exact
semantics and error cases; tests to add; acceptance criterion.
`make gate` green at the end of each WP. -->

## Closing

<!-- Replace every placeholder. -->

- [ ] Current-state docs: `docs/<page>.md`
- [ ] Decision file: `docs/decisions/D<issue>-<slug>.md`, `topic: <topic>`, or `none` (no architectural or contract choice)
- [ ] Traps: fixed with a test or a comment next to the code, or `none`
- [ ] Release impact: `<none | describe>`
