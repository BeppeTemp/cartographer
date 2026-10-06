---
topic: data-plane
---

# D321 — `active` means the page is valid, everywhere; waiting work is declared

**Decision.** The `active` status family is never open work, in a journal or a map. A journal declares what open means through `open_statuses` (`map_create kind: journal` writes `[open, in-progress, blocked]`); a KB that reads `active` as open lists it there. A concept declaring `review_after` today or later is not `stale_open` (still open, listed in Work, not stale), and `waiting_on` records who or what blocks it. `status_semantics` warns on write, and `kb_review` proposes the migration (`status_reclassify`) from deterministic signals.

**Why.** `openPhase` treated `active` as open in a journal only: a rule no author, agent or Work panel reader could see. On a ~760-concept KB the Work panel listed 92 "active" concepts, mostly assessments, registers and resolved incidents. The only escape for work blocked on a third party was touching `timestamp`, which hides its real age. The cost: a breaking change for KBs relying on the old reading, named in the release note.

**Alternatives rejected.** A grace period with a warning only: a warning without action is ignored. A per-KB opt-in to the new meaning: the confusion is the default, so the default must change. Silent migration by the server: it never decides content (D14), hence a review kind the doctor applies and the operator confirms. A new `suspended_until` field: `review_after` already means "come back after this date".

**Consequences.** `openPhase` and `workPhase` must keep agreeing, and `reviewSuspended` serves both `stale_open` and Work's `stale`. The review kind `status_reclassify` and the check `status_semantics` share `activeNotOpen`. A past `review_after` suspends nothing. Journal templates and the bundled kb-doctor skill must not prescribe `active` for work.
