---
topic: skills-services-secrets
---

# D1256 — Ingest pipeline skills

**Decision.** A large ingest campaign is run by two bundled skills: the new `cartographer-kb-ingest-plan` (a large model plans once, gets the operator's approval, drives a pool of subagents over the `work_list` queue and reviews a sample) and a **unit mode** in `cartographer-kb-ingest` (a small model executes one `IngestUnit`). `cartographer-kb-import` is unchanged (D74): the planner proposes it for a source that is already a wiki. The planner is not served by `kb_guide` (D1100), since it spawns subagents and its checkpoint needs a conversation; the `ingest` procedure, which is served, gains the unit mode. Unit mode skips the page-count checkpoint, because the planner's checkpoint approved the whole campaign, and is capped at 30 lines by a test. A script stored as an asset of a plan is never run without the operator's approval of that run (D6). The sample review is ceil(10%) of the done units, at least 3, always the first done unit of each Map and every unit whose digest reported a gap or contradiction. The lease is a whole-day `review_after` date with the semantics of the code: a passed lease leaves an `in-progress` unit as a plain open entry, so both skills define claimable as `open`, or `in-progress` with `review_after` before today (UTC).

**Why.** Planning and executing one source are different jobs, and a small model must not carry the planner's text; the unit mode sits before the Procedure so a model that stops reading early still has it. Anyone who can write to a KB would otherwise run code on its readers' machines.

**Alternatives rejected.**
- One skill with a planner mode: the executor would carry text it never uses.
- Making `cartographer-kb-import` the planner: it maps a foreign wiki with a scaffold, a different job.
- Serving the planner through `kb_guide`: a client with no subagents cannot follow it.
- Reading a passed lease as a `review` entry: it changes `work_list` for every KB (D1201).

**Consequences.** Both skills follow the field names and statuses of D1201. `ingest_setup` is not in `tools/list`, so the planner carries a pointer for clients that cannot call it. Extends D279 and D958.
