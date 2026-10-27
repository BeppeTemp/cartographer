---
topic: control-plane
---

# D1197 — facet_sprawl proposes a closed tags vocabulary that map_update and kb_repair apply

**Decision.** The `facet_sprawl` observation carries a deterministic `Proposal` (`field_values.tags`: values, mapping, drop) built from clusters of tag spellings, and `invalid_field_value` gains a list-aware fix, `list_value` (`From` to `To`, empty `To` removes), reported per bad item. A removal is applied only by an explicit `kb_repair`; the write path and the background repair apply renames only.

**Why.** The observation only listed the most used tags, so a delegated doctor session could not close it and the item came back every session; `missing_value_contract` already showed the shape that works (a ready proposal, `map_update`, `kb_repair`). The tags field is a list, which the scalar `set_value` fix could not repair. Costs: once declared, the vocabulary is closed, so a new tag needs a `map_update` first.

**Alternatives rejected.** Edit distance or co-occurrence to merge more spellings: not deterministic enough to explain to an operator, and a wrong merge rewrites pages. Storing the drop list in the contract: a new contract key for a one-shot action. Applying removals unattended like other `AutoRepairSafe` fixes: it would silently erase a tag an agent writes before the `map_update` that declares it, turning "declare first" into data loss. A cluster threshold of 1: every singleton would enter the vocabulary and the sprawl would stay.

**Consequences.** `Fix` has a `from` field and `Proposal` a `drop` list; the keep predicate `unattendedFix` (`internal/mcpserver/tools_repair.go`) must stay on every unattended path. `map_update` replaces every `field_values.*` and `value_synonyms.*` key, so the doctor merges with the existing ones. `value_synonyms` is map-wide: a vocabulary tag that belongs to a built-in status family can leave an item without a fix, never with a wrong one.
