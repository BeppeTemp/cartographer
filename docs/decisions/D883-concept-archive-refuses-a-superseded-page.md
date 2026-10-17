---
topic: control-plane
---

# D883 — `concept_archive` refuses a superseded page

**Decision.** `concept_archive` refuses a page whose `status` is `superseded`, before writing anything, and names `concept_move` as the way to relocate it. A `deprecated` or `archived` source is still allowed.

**Why.** A superseded page is already retired, and its status and `superseded_by` tell one story. Overwriting the status with `archived` or `deprecated` left `superseded_by` on a page that said it was archived, and the Atlas's retired view (D820) then showed an archived row "replaced by" a successor. The cost: archiving a superseded page into the archive map now takes `concept_move`, which keeps the source index entry unless the map declared `require_index_entry` (D344).

**Alternatives rejected.**
- Archive it keeping `status: superseded`: the log entry would read `concept_archive: <src> -> <dst> (superseded)`, and `kb.Retirement.Subject` takes any entry with status `superseded` for a supersede, whose subject is the source: the retired view would match the wrong id.
- Clear `superseded_by` when archiving: it throws away the successor, the one fact a reader of a retired page needs.

**Consequences.** A future status that, like `superseded`, carries its own fields must be weighed the same way before `concept_archive` may overwrite it. The refusal is pinned in `TestConceptArchive_Refusals`.
