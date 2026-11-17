---
topic: control-plane
---

# D2206 — the write gate refuses a page whose folder is not a declared Map

**Decision.** When a KB declares at least one Map (`_map.md`, or the legacy `_archive.md`), a write that puts a page in a top-level folder with no descriptor is refused by the write gate as `unmapped_folder`, naming the closest declared Maps and `map_create`. `unmapped_folder` becomes an `OnWrite`, `AvoidableAtWrite` check: `lint.ScopedCheck` reports it for the folders of the pages a write touched. The whole-KB lint reports the same finding with the same message. `gate_check` applies one contradiction rule to both scopes: an empty `changed_ids` now blocks on every open, non-gap contradiction (a `scope` narrows by the contradiction's path), and a failing result carries `blocked_by`, the first two findings of each source that make it fail.

**Why.** A typo in a map name (`concept_write id=nomap/zz`) silently created an orphan folder that lint later flagged and `gate_check` failed on with nothing named. The check was already known and had a fix; only the write path did not look.

**Alternatives rejected.** Auto-creating the Map on write: a typo would then declare a Map. Refusing in every KB: a KB with no Map at all is a legacy layout with nothing to compare against, so it stays unjudged. Leaving the whole-KB gate without a commit gate: it reported `pass: true` over open contradictions the scoped gate blocked on.

**Consequences.** A write that was accepted before (a new page in an undeclared folder of a KB with Maps) is now refused: declare the Map with `map_create` first, in a call of its own (`concept_batch` has no map operation). `concept_move` is exempt (a rename of a map into a new name still lands without a descriptor, as the services namespace migration does). Editing a page already in an undeclared folder is not blocked: the finding is in the baseline. `gate_check` on a whole KB with open contradictions now answers `pass: false`.
