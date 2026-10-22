---
topic: control-plane
---

# D1099 — The only live page of an archive is no orphan, and `concept_write` takes `index_entry`

**Decision.** (1) `orphan` is not emitted for a concept that is the only live (not `deprecated`, `superseded`, `archived`) concept of its archive, nor for the only live concept of the KB. The count is over the whole KB graph, whatever the scope of the call or the caller's visibility; an archive is a first-level directory of `data/` (`services/` is not one). `orphan` stays `AvoidableAtWrite`: what changes is when the finding exists. (2) `concept_write` accepts `index_entry: true` with the `concept_new` semantics (D714): creation only, refused before anything is written on an existing id or with `if_match`, the entry under `## New pages`, `curated_indexes` in the response, a failed index edit after the write a note. The write-gate refusal text names `concept_write` among the tools that take it.

**Why.** D907: the gate refuses only what a write can avoid. The first page of an archive cannot link to anything, so the one finding blocking it was not avoidable; and the second blocker, the index entry, existed on two tools but not on the one a generic agent reaches for first. The refusal named `concept_new`, which needs a template a fresh KB does not have. A host that gives the model only tools (no managed block, no skills) could therefore never write its first page.

**Alternatives rejected.**
- Always adding an index entry on creation: already rejected in D714 (the caller asks explicitly).
- A way to bypass or accept a finding: removed by D907.
- Leaving the exemption off and sending the agent to a two-page `concept_batch`: fails for a one-page start.
- Exempting every archive-top page with no incoming link: reverts D306 (an isolated top-level page is a node connected to nothing).

**Consequences.**
- Once a second live page exists and nothing links the first, a whole-KB `lint` reports the first as `orphan`. The write that added the second is not blamed (it did not write the first), and a second page that links the first clears both. An archive whose other pages are all retired exempts its last live page.
- `index_entry` on a satellite of an expanded concept adds the entry to the map's index, as `concept_new` does; the dossier's own index is not extended.
- Authorization is unchanged and deliberately not widened or narrowed: no code reads `index_entry` for authorization, so it edits the map's index on the concept-level grant, as `concept_new` and `concept_batch` already do, whereas `index_patch` needs the map-level grant. Closing that gap for all three tools is a separate decision.
- `ScopedCheck` builds the live census lazily, only when an orphan candidate asks, so an ordinary write pays nothing; `Run` and `ScopedCheck` share `orphanFinding` and must agree (pinned by `internal/lint/lone_live_test.go`).
- `changes_since` `became_orphan` is an in-degree delta of its own and is unchanged.
