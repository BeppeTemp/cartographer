---
topic: control-plane
---

# D331 — `kb_status.conformance` counts every repairable finding, beside the conformance-only `fixable`

**Decision.** `conformance.repairable` is `{check: count}` of every finding the
caller can see that carries a `fix`, whatever the check. `fixable` keeps
counting only the conformance checks. The kb-doctor skill plans its mechanical
pass from `repairable`.

**Why.** `fixable` was the only number a doctor session read before running
`kb_repair`, and it ignored fixable checks that are not conformance debt
(`stringified_list`, `title_h1_mismatch`, `legacy_path`, …): a real KB showed
`fixable: 0` with 15 repairable findings. The skill also hard-coded a list of
checks that had fallen behind `lint.FixableChecks`. A per-check map names the
`check` argument `kb_repair` takes, so the skill needs no list of its own.

**Alternatives rejected.**
- *Widen `fixable` to every check*: it would no longer match the conformance
  `findings` it sits beside, and `fixable` already appears in the doctor nudge
  and in session summaries with its D290 meaning.
- *Promote the missing checks to conformance checks*: they would then count
  toward `doctor_suggested`, and `reciprocal_link_item` is an opt-in, not drift.

**Consequences.** A check that gains a `Fix` shows up in `repairable` with no
change here. `doctor_suggested` still depends on conformance debt only.
