---
topic: data-plane
---

# D273 — Knowledge gaps are Contradiction concepts with a reserved, non-blocking kind

**Decision.** A gap is a `Contradiction` concept whose `contradiction_kind` is
`missing_context` (the KB lacks information it should have) or `open_question`
(a question raised and not yet answered). `kb.GapKinds` / `kb.IsGapKind` name the
two kinds; `commit_gate` and `gate_check` never block on a gap, whatever its
`involves`, and `involves` is optional for one. `kb_status` counts open gaps in
`open_gaps` (`total`, `by_kind`, 10 newest in `recent`) and excludes them from
`open_contradictions`; `contradiction_report` takes a `kind` filter (an exact
kind, `gap`, or `contradiction`).

**Why.** A KB could record that two claims disagree but not that something is not
known, so an unknown stayed prose no tool could find. The Contradiction lifecycle
(open → resolved with a resolution), `contradiction_report`, `conflict_resolve`
and the `kb_status` count already fit; only the gate semantics differ, since an
open question must not block the writes that answer it.

**Alternatives rejected.**
- A new `Gap` concept type with its own tools: duplicates a lifecycle that is
  identical, and adds tools to a public contract.
- Letting gaps block like contradictions: writing is how a gap is answered, so it
  would deadlock the KB on its own to-do list.
- Reusing `search_misses` (D247): it shows what readers hit, not what writers
  already know is missing.

**Consequences.** `open_contradictions` is narrowed: it no longer counts gap
kinds (no KB used them before). The two kinds are reserved: any other
`contradiction_kind` keeps blocking. `kb_status` is the agent's view of gaps; the
advanced tools (D123) stay callable by name.
