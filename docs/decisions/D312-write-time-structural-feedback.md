---
topic: control-plane
---

# D312 — a write answers with the structural findings it caused, from a scoped lint

**Decision.** `concept_write`, `concept_new`, `concept_patch`, `concept_batch`, `concept_move`,
`supersede` and `index_patch` return, beside the frontmatter findings of D289, the body, link and
light graph findings of `lint.ScopedCheck`: `broken_link`, `duplicate_link`, `bare_link_list`,
`reciprocal_link_item`, `unknown_placeholder`, `forbidden_term`, `orphan`, `broken_relation`,
`link_to_retired` and `index_incomplete`. `ScopedCheck` reads the cached link graph and the written
concepts' own files, not the KB: about 10 ms on 1,000 concepts (`BenchmarkScopedCheck`), against
180 ms for a whole-KB lint. `gate_check` with `changed_ids` and no `scope` runs the same scoped lint
over exactly those concepts and says so with `lint_scope`; an empty `changed_ids` stays the
whole-KB gate (D318).

**Why.** An agent writing 60 pages in three days left 10 findings it never saw: a move into
`archive/` retires a concept its linkers still cite, a new journal entry is an orphan missing from
a curated index. They surfaced only at the next `lint`, by which time nobody remembered the write.
Findings are cheap to fix at the write and expensive afterwards.

**Alternatives rejected.**
- A full `lint.Run` after each write: 180 ms on 1,000 concepts, paid on every write, for findings
  that are mostly someone else's.
- Reporting every finding on the neighbours: a write would answer for the KB's whole backlog. A
  neighbour is reported only for what the write introduced: a page linking an ID that is gone, a
  retired concept the written one links.
- `link_to_retired` on the linking page, as the plan wrote it: D313 reports it once, on the retired
  concept, so the scoped check reports it there, for the retired concept written or linked.
- Running `open_marker` and `closed_with_open_items` in `ScopedCheck`: they are frontmatter-driven
  decay checks `CheckConcept` already returns, so a write response had them before D312.

**Consequences.** `cut_concept`, `island`, `map_misfit`, `map_oversize`, `facet_sprawl`,
`missing_value_contract` and `source_uncited` need the whole KB and stay with `lint` and
`gate_check`: a clean write response does not mean a clean KB. The neighbour expansion is capped at
200 per direction (`scopedNeighbourCap`) so a hub does not turn a write into a full lint; past it,
the remaining linkers wait for the next lint. `ScopedCheck` shares `linkFindings`, `orphanFinding`
and `structure.linkToRetired` with `runChecks`, so the two cannot report different findings for the
same body. A first write of an unlinked concept now answers `orphan`, which is true and is what the
agent should fix. The `tools/list` budget (D285) is tight: the description additions are one clause
each, the detail is in `docs/control-plane.md`. A benchmark ages its fixture past the graph cache's
racy window first (`settleGraph`), or it measures re-reading files written a moment ago.
