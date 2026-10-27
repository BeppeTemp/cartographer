---
topic: control-plane
---

# D1078 — Review kinds for unpromoted lessons and unclassified index entries

**Decision.** `kb_review` gains two debt kinds, `lesson_unpromoted` (an archived journal entry that no live page of a non-journal map links and that does not carry the header `> Archived ...: no durable lessons.`) and `index_unclassified` (a curated map index with entries still under `## New pages`, one item per index). A lesson counts as carried when a live non-journal page links the archived entry; the "no durable lessons" header is the only exemption.

**Why.** Whether lessons were carried and where a new page belongs in a curated index are judgements the server cannot make (D14), so they are review kinds, not lint, which has no severity and no exceptions (D947, D907). `harvest_candidate` already covers closed entries not yet archived; what stayed unseen was the entry archived without the harvest, so the two kinds are disjoint by status. Both close on page state, never on a dismissal (D907).

**Alternatives rejected.** Lint checks: no definite fix exists for either. Widening `harvest_candidate` to archived entries: it would stop being disjoint and its age rule is meaningless for an archived entry. Also listing `## Moved here`: not requested, a different heading and intent.

**Consequences.** A KB with unharvested archives or unfiled index entries gets items it did not have, and `doctor_suggested` may turn true (additive, no on-disk change). The header wording is the doctor skill's step 5b: the kind and the skill change together. The heading is the shared constant `kb.NewPagesHeading`, so the writer (D714) and the kind cannot drift.
