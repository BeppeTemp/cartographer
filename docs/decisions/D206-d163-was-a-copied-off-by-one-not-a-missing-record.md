---
topic: project-governance
---

# D206 — D163 was a copied off-by-one, not a missing record

**Status: resolved (2026-09-14).**

**Context.** The gate added by [D204](D204-the-documentation-gates-live-inside-make-test.md) — every
`D<n>` cited in Go source must resolve to a file — found exactly one violation on
its first run: `D163`, referenced eight times across
`internal/provisioning/provisioning.go`,
`internal/provisioning/provisioning_instructions_test.go`, `docs/sync.md` and
[D183](D183-keyed-session-global-directives-make-a-cross-kb-prose.md), always as
an established decision and always for the same idea — "D163's metasyntax trap":
that a syntax Cartographer recognises inside a file the operator is also expected
to *document* must not fire on text that merely looks like it.

`163` is a gap in the numbering, alongside 4, 6, 7, 11 and 130. Nothing was ever
filed there.

**Decision.** The eight references are renumbered to **D162**, whose second
defect and matching alternative *are* that decision: `{{repo:<name>}}` written to
show the generic form was indistinguishable from a real reference and produced
eleven warnings per sync, resolved with the authoritative escape `{{\repo:...}}`
plus a metasyntax heuristic that silences the warning for a key of the form
`<name>` or `...` while leaving the text verbatim. `internal/provisioning/expand_test.go`
already labels that work "D162 WP3". No `D163` record is written, and the number
stays a gap.

**Alternatives rejected.**

- *Write the record from the citations.* This was the plan until D162 was read
  in full. The eight comments describe the trap consistently enough that a
  plausible `D163` could have been written — and it would have duplicated a
  decision that already exists, leaving two records for one choice and no way for
  a reader to tell which one the code meant. It is worth stating plainly: the
  citations were coherent, which is exactly what made the wrong fix look correct.
- *Retire the number the way [D188](D188-retired-without-ever-being-written.md)
  was retired.* D188 was retired because nothing depended on it and nothing could
  be reconstructed. Here the dependency is real and the target exists, so retiring
  would have left eight comments pointing at a deliberate hole.
- *Leave the references and allow-list the number.* That is what the gate did for
  one commit, as the honest interim state. Kept, it would have normalised a
  reference that resolves to nothing.

**Consequences.** `KnownDanglingDecisions` is now empty, and it is checked in both
directions: a stale entry whose record exists fails the test, because an
allow-list nobody prunes is how a gate quietly stops meaning anything. `D163`
must not be reused — the same reasoning as D188, weaker only in degree: released
`CHANGELOG.md` entries and the git history contain comments that discussed "D163"
as the metasyntax decision, and a future `D163` about something else would make
them read as a description of it.

This is also the first thing the new gates caught, and it is the argument for
them in one line: the reference had been wrong for weeks, was copied seven more
times, survived a documentation refactor, and nothing anywhere reported it.
