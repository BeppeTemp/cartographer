---
topic: control-plane
---

# D229 — A map contract can change after creation

**Decision.** `map_update` changes the lint contract of an existing map —
`require_index_entry`, `required_fields`, `required_fields_by_type`,
`machine_path_allow_prefixes` — and nothing else in its descriptor. A dead
link in a map's `index.md` is a `broken_link` finding whether or not the map
opted in to `require_index_entry`; completeness (`index_incomplete`) stays
opt-in.

**Why.** The contract could be set only by `map_create`, and `_map.md` is
reserved from `artifact_write` and `concept_write`. A map created before its
contract existed could therefore never opt in, so `concept_move` never
maintained its index and `lint` never looked at it: after 21 concepts moved
out of three such maps, every index still linked the old IDs and lint reported
nothing (#320). A broken link is broken regardless of a promise of
completeness; tying its detection to the contract hid exactly the maps that
had drifted the longest.

**Alternatives rejected.** Letting `artifact_write` or `concept_write` edit
`_map.md`: it would open the descriptor's structural keys (`kind`,
`ontology_mode`, `concept_types`) to a free-form write the server cannot
validate as a contract. Rewriting every `[[id]]` in every map index on
`concept_move`, contract or not: index maintenance was made opt-in by D160 on
purpose, since a curated document is the operator's prose; reporting the dead
link is enough to make the drift visible. Classifying `map_update` as an
advanced tool: the agent that tidies a map's index is the caller that needs
it, and `map_create`/`map_delete` are agent-visible for the same reason.

**Consequences.** Absent keys are left as they are and an empty value removes
the key, so a descriptor updated to a contract reads exactly as if
`map_create` had written it. A legacy `_archive.md` is refused, not rewritten
(D77 keeps that form read-only). A map index missing altogether is still not a
finding for a map without the contract.
