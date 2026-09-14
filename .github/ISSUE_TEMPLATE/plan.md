---
name: Plan
about: Self-contained implementation plan (design → implementation handoff)
title: 'Plan: <title> (D<n>)'
labels: plan
---

> **Status**: approved, not implemented. On completion: add the decision file
> `docs/decisions/D<n>-<slug>.md` (`make decisions-new`, then
> `make decisions-index`), update the affected current-state docs
> (`docs/index.md` §Documentation maintenance rules), then close this issue from
> the implementation PR (`Closes #<n>`).

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
- [ ] Decision file: `docs/decisions/D<n>-<slug>.md`, `topic: <topic>`
- [ ] Release impact: `<none | describe>`
