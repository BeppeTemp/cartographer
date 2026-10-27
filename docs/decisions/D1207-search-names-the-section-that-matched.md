---
topic: control-plane
---

# D1207 — Search names the section that matched

**Decision.** A `search` hit may carry `section`, the `##` heading line exactly as `concept_read`'s `section` accepts it, and then its `snippet` is that section's excerpt; a hit on a page that must be read whole carries `read: "whole"` and never a `section`. A page is read whole when it lives in a journal, or when its type is listed in the new optional `_map.md` key `read_whole`. Each `##` section is indexed as a unit in a second FTS table (`sections_fts`, schema version 4); the page ranking is unchanged.

**Why.** `search` returned whole pages while `concept_read` could already return one section, so an agent could not tell which section answered its query and read the page: in a measurement over 35 real tasks the page body is 88% of a `concept_read` payload. The same measurement showed quality dropping from 12/12 to 6/12 when procedures, runbooks and incidents are read by section, so the server must also say which pages are not to be cut. It ships no domain vocabulary, so "read whole" comes from the journal kind (already in the contract) or from a list of types the KB itself declares.

**Alternatives rejected.**
- A hard-coded type list (`Runbook`, `Procedure`): breaks "the server ships no domain vocabulary".
- A per-page frontmatter flag: cannot be enforced, and the 12/12 versus 6/12 difference is not something to leave to each page.
- A `sections` column in `concepts_fts`: would change its weights and its `snippet()`; a separate table leaves page ranking untouched.
- Ranking pages by their best section: out of scope, the page order is deliberately unchanged.
- Resolving the section inside `keywordHits`: `graph_context` seeding and the write-time `repeated_fact` lookups would pay for it.

**Consequences.** `section` is omitted when naming it saves nothing: body under 2048 bytes, best section 80% or more of the body, no `##` heading, or a match only in the title, frontmatter or assets. The sqlindex stays disposable: opening a version-3 database drops both FTS tables, empties `concepts` and reindexes on the next reconciliation; until then a hit simply has no `section`. Every new FTS table must be dropped in `migrate` and created in `createSchema`. The heading is the contract with `concept_read`: `okf.Level2Sections` drops a repeated title because `okf.ExtractSection` could never return it, and the handler checks the heading resolves before naming it. `read_whole` is a hint, not a lint rule, so dropping an entry is no loosening and needs no `reason`. The in-memory backend uses the same weights (heading 3, body 1) with a bm25-like score, so both backends agree on ordinary queries. The maintainer re-runs the 35-task benchmark locally after the merge and records the numbers here; it is not in CI.
