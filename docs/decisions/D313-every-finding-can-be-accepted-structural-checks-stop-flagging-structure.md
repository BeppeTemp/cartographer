---
topic: data-plane
---

# D313 — Every finding can be accepted; structural checks stop flagging structure

**Decision.** `map_oversize` is accepted in a map's `_map.md`, counts top-level concepts only against `oversize_concepts` (default 50) and never fires on a journal. `link_to_retired` is one finding per retired concept, on the retired concept, never counting its `superseded_by` successor. `cut_concept` ignores an expanded concept's own satellites and journal entries. `missing_value_contract` skips date-valued fields; `closed_with_open_items` skips procedure sections; `open_marker` skips headings and, in an open concept, table rows. `secrets_on_non_service` is `info`. A resource match is a `duplicate_candidate` only with title similarity of 0.3. A built-in list of conventional tool paths is always allowed by `machine_path`. `kb_status.conformance.acceptability` and `/api/lint` say where each reported check can be accepted, and the Observatory shows it.

**Why.** D306 promised every finding is fixed or accepted, yet `map_oversize` could not be accepted at all, and on a real KB a cleaned state lasted a day: the checks that came back flagged how the KB is shaped (a service page with journal notes hanging off it, an archive move that leaves historical mentions, a runbook checklist) rather than a defect an agent could fix.

**Alternatives rejected.**
- Keep `link_to_retired` per linker and let each accept it: seven findings for one decision, and none of the linkers can fix them without erasing history.
- Count satellites in `map_oversize` and only raise the threshold: it measures the wrong thing, a dossier is one concept to the map.
- Exempt journals from `cut_concept` by map name: the contract `kind` is the declared fact, a name is a guess.
- Put the conventional-path list in each map contract: every KB would repeat it; one built-in list that a map extends is enough.
- Derive `zombie_work` from `link_to_retired` findings: accepting the latter would hide open work still pointing at the retired page, so it reads the link graph.

**Consequences.** `map_oversize` counts drop on KBs with expanded concepts, and `oversize_concepts` is a new contract key (`map_update`). `link_to_retired` findings move from the linker to the retired concept, so an existing `lint_ignore` on a linker no longer matches. `secrets_on_non_service` leaves the warning count, `duplicate_candidate` and `machine_path` fire less often. `CheckAcceptability` reads the same tables as `lint_ignore_invalid`, so it cannot drift. The Atlas bundle is rebuilt.
