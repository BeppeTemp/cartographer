---
topic: data-plane
---

# D826 — a retired concept in a live map gets no index or orphan instruction

**Decision.** A concept whose status is retired (`kb.IsRetired`: `deprecated`, `superseded`, `archived`) is skipped by `index_incomplete` (`checkCuratedIndex`) and by `orphan` (`orphanFinding`), in the full lint and in the scoped write-response lint alike. `status: archived` stays allowed on any concept, with no new check for it outside a journal.

**Why.** `link_to_retired` and `index_lists_retired` push links away from a retired page, and the default auto-repair drops its index entry (`config.DefaultAutoRepair`). Asking for the entry back or for an incoming link contradicted them: the agent added the line, the repair dropped it, the next lint asked again. The `archived` status is not confined to journals: the kb-doctor harvest archives journal entries in place (D322, "nothing is deleted or moved"), and `concept_archive` (D344) writes it on a page it moves into a non-journal archive map. A check for `archived` outside a journal would fire on both.

**Alternatives rejected.**
- A new `retired_in_live_map` finding suggesting `concept_archive`: it would fire on every harvested journal entry, and a "move it" instruction contradicts D322.
- A warning for `archived` outside a journal: it would fire on the output of `concept_archive` itself.
- Treating every non-active status as retired for these two checks: `disputed` and `draft` are live pages and keep their index and orphan contract.

**Consequences.** A retired page left in a live map is neither asked into an index nor asked to be linked; `link_to_retired` and `index_lists_retired` remain the only instructions about it. A new check that asks for a link to, or an entry of, a concept has to decide what it says about a retired one, and say it consistently with these three.
