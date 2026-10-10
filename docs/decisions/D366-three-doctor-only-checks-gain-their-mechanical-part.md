---
topic: data-plane
---

# D366 — Three doctor-only checks gain their mechanical part, and join the default auto-repair

**Decision.** `index_lists_retired` carries `drop_index_entry`, `link_to_retired` carries `retarget_links` and `title_quality` carries `set_value title`, each only where the result is unique. All three are `AutoRepairSafe` and in `config.DefaultAutoRepair`, so the background repair applies them like `duplicate_link`.

**Why.** On an imported KB every info finding waited for a kb-doctor session that may never come, yet part of three checks needs no judgement: the doctor skill itself says "remove the entry" for a retired concept in an index, a retired concept with a live `superseded_by` has an obvious place for its links to go, and a title's decorative characters have one stripped form. The rest of each check stays judgement (D356: no invented content).

**Alternatives rejected.**
- *Leave them to the doctor.* Keeps the findings forever on a KB nobody runs the doctor on.
- *Retarget to a successor of a successor.* A chain A to B to C where B is retired is judgement (B may be retired for a reason that does not transfer); only one hop to a live concept is applied, and B's own finding retargets its linkers when it is its turn.
- *Drop an index line that cites other concepts.* The other links may be the reason for the line; same rule as `concept_archive`: only a line whose single link is the retired concept.
- *One finding per index with several fixes.* A finding carries one `Fix`; one finding per retired concept keeps that and lets each be accepted or skipped on its own.
- *Emit the link finding on each linker.* It would break D313 (one decision, one finding, accepted on the retired concept).

**Consequences.** `Fix` gains `targets` (the concepts a fix edits when they are not the finding's own, used by `retarget_links`), so `Fix` is no longer comparable with `==`. The default `auto_repair` list grows by three checks: a KB relying on the default gets these repairs on the next heartbeat, in one revertible commit; `auto_repair: [...]` opts out per check. `index_lists_retired` findings are now one per retired concept listed, not one per index. Rewriting links never drops an edge (D309); every fix is idempotent on its own output (D355).
