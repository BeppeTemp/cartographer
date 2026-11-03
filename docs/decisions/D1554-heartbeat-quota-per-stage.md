---
topic: control-plane
---

# D1554 — The heartbeat quota bounds each stage, and the concept stage rotates

**Decision.** The 500-concept bound of the background repair (D355) applies to each stage separately: the concept-local stage, every cross-concept, map and artifact check, and the second pass over the concepts the cross-concept stage touched. There is no counter shared between stages. In the concept stage, only findings an unattended run may apply (`unattendedFix`) select a concept, and when more than the quota qualify, the next run resumes after the last concept the previous one processed (in-memory cursor, not persisted). A seeded concept that did not move is counted as `skipped`.

**Why.** D355 and the comment on `autoRepairLimit` said "all checks together", but the code and its tests bound each stage; a shared counter would starve the later stages in a KB with many local findings. The concept stage sorted ids and cut the first 500, so concepts whose only fix is a removal (never applied in the background, D1197) or that stay stuck took the same slots every run and the concepts after them were never repaired.

**Alternatives rejected.** One counter for the run: starves cross-concept and artifact checks behind the local stage. A persisted cursor: a restart rerunning from the start costs one run, and a file would need cleanup and versioning. Dropping stuck concepts from the sort: they would come back every run, still ahead of the rest.

**Consequences.** One run can rewrite 500 concepts per stage, not 500 in total; the Maintenance panel and `auto-repair-log.jsonl` count per check. D355's "quota of 500 concepts" reads per stage. A change to the stages must keep the cursor in the concept stage only.
