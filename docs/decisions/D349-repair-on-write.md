---
topic: control-plane
---

# D349 — A write repairs the mechanical findings of the concepts it wrote

**Decision.** After a content write, inside the same handler and so the same
commit, the server applies the KB's `auto_repair` fixes to the concepts the call
left on disk, and reports `repaired: [{check, count}]`. Per-KB `repair_on_write`
(tri-state: absent follows `auto_repair`, `false` = timer only) opts out.

**Why.** Findings already travel in write responses (D312), but agents treat
`info` findings as optional: after about 120 page writes by four subagents seven
`duplicate_link` findings stayed in the KB until the daily heartbeat (D323).
`gitWrap` runs handler and commit under one lock, so a second `WriteConcept` in
the handler lands in the same commit and nothing can interleave; `applyFixes`
already applies any `Fix`, so no new fixer exists.

**Alternatives rejected.**
- Leave it to the timer: the findings sit in `kb_status.conformance.repairable` for a day.
- Repair neighbours too: a write must not touch pages the agent did not name (same scope rule as D312).
- Run `broken_link` / `reciprocal_link_item` on write: they drop or rewrite links (D309) and the mutual-pair guard needs cross-concept state a single write lacks; they stay with the timer and `kb_repair` even when listed.
- Change the tool descriptions to mention `repaired`: the D285 `tools/list` byte budget has no headroom; the existing "Returns ... findings" text stands and `docs/control-plane.md` documents the field.
- A plain bool for `repair_on_write`: absent must follow `auto_repair`'s own default (D323), so a pointer.

**Consequences.** Written content can differ from what the agent sent, only for
the listed mechanical checks. A repair error never fails the write (D289
contract). A check whose fix is fatal or needs a person (`partial`) is rolled
back for that concept and its finding stays. The repair relies on running inside
`gitWrap`; the single-commit assertion in `repaironwrite_test.go` fails if it
does not. Works in stdio too: it follows a write the client asked for.
