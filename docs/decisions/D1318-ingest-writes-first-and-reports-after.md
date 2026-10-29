---
topic: skills-services-secrets
---

# D1318 — Ingest writes first and reports after

**Decision.** The `cartographer-kb-ingest` procedure no longer stops for approval when a plan touches more than 5 pages or creates more than 2. It writes, then reports pages patched and created, gaps, contradictions, the commit each write produced and the revert path (`concept_history`, then revert those commits; `repair_revert` is for `kb_repair` only). It asks first **only when the caller asks for a preview, a plan or a dry run**. The text is the same for every client: the bundled skill and the `kb_guide` procedure are one file (D1100). Unit mode (D1256) says only that it never asks for approval and that pages outside `targets` are not covered. Amends D279 (checkpoint).

Step 4 is bounded: one `search` per subject, at most three candidate pages read (a page whole only when the hit says so or it is a procedure, runbook or incident), grouped subjects read through one `index_get` or `concept_list`; with no owner at the bound, the subject has none and the page is created (`kb_review` catches a duplicate later).

An unknown on a strict map that does not list `Contradiction` is an open checkbox in `## Open questions` of the owning page (else the template's follow-up section, else the Source body), after reading the map with `map_list`. It is not equivalent to a `Contradiction` (not counted in `kb_status.open_gaps`, does not block the gate), so the report lists each one. A real contradiction on such a map leaves the claim as is and is recorded the same way, flagged for the operator. The write gate (D907), D273 and the rule never to overwrite a claim are unchanged; no tool or field is added.

**Why.** A server-only usability test: 4 of 4 runs wrote nothing, because a non-interactive caller never answers the checkpoint, and every write is a revertible commit, so a number cannot tell a safe large write from an unsafe small one. Agents also spent 14-18 searches and reads before the first write and improvised differently when the map refused `Contradiction`. The write refusal already lists the declared types (D1314), so nothing changes server-side.

**Alternatives rejected.**
- Raise the thresholds: still stops a non-interactive caller.
- A new tool to read a map's palette: `map_list` already returns `concept_types`.
- Widen `concept_types` automatically: the contract is the operator's.
