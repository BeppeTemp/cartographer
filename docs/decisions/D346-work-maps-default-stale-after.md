---
topic: data-plane
---

# D346 — A map that holds work is stale after 30 days by default

**Decision.** `lint.EffectiveStaleAfter` is the single place that decides `stale_open`'s threshold: an explicit `stale_after: N` wins, `stale_after: 0` is an explicit off, a journal defaults to 60 days, a map that holds work (`open_statuses` set, or an open value in its `status` vocabulary) to 30, a reference map to none. The default is computed at read time and never written to `_map.md`; `map_list` and the `map_update` response show the effective value.

**Why.** Open Tasks in a work map never went stale, because only journals had a default (D297). Computing it at read time means existing KBs get it with no migration and a later change of the default needs none. "Holds work" is read from the contract's own open-phase signals because no list of work types exists. `map_update` could not express "off" with the old "empty removes the key" rule and an integer schema cannot carry null, so `0` writes an explicit off and `-1` is the reset.

**Alternatives rejected.** Keying on a concept type such as `Task`: no registry of work types, and a Task map without a status vocabulary has no open phase. Writing the default into `_map.md` on `map_create`: needs a migration for every existing KB. A per-map value in `kb_status`: it is KB-wide, and `stale_count` counts `review_after`, a different signal; `map_list` is the per-map place.

**Consequences.** Behaviour change for existing KBs: open concepts older than 30 days in a work map start showing `stale_open` (info) and `stale: true` in `work_list`; opt out with `stale_after: 0`. `stale_after: 0` used to be a malformed key and, via `map_update`, a deletion. `kb_status.stale_count` stays `review_after`-based. Journal defaults beyond the threshold build on `EffectiveStaleAfter`.
