---
name: cartographer-kb-ingest-plan
description: Use when the operator wants to ingest MANY sources (a documentation corpus, a ticket archive, a set of feeds) into a Cartographer KB as one campaign - plan it once (Maps, subject ownership, one self-contained unit per source), get the plan approved, then drive a pool of subagents over the unit queue and review a sample. For one source use cartographer-kb-ingest.
version: "1.0"
---
# KB Ingest Plan - Skill

## Purpose

A large corpus is ingested in two halves: a strong model **plans** it once (Maps, which concept owns
which subject, one self-contained `IngestUnit` per source), and cheap or local models **execute** one
unit each with `cartographer-kb-ingest` in its unit mode. Everything lives in the KB as concepts
(`IngestPlan`, `IngestUnit`, `Source`), so the campaign is resumable and shared by several agents.
The server never calls a model and never fetches (D28, D278): you read sources with the client's own
capabilities. `cartographer-kb-import` is unchanged (D74): use it for a source that is already a wiki.

**A session starts only when the operator asks for it** (D704). Delegating reading and unit
execution to subagents moves cost, never decisions: every decision below is yours.

## When to use

Many sources, or one large collection, that become KB knowledge. One source: `cartographer-kb-ingest`.
A foreign wiki or folder of notes to map wholesale: `cartographer-kb-import`.

## Tools not in `tools/list`

`ingest_setup` is not advertised by `tools/list`; the server serves it when called by name. If your
client cannot call it, stop and tell the operator: the unit and plan templates are missing. If the
server has no `ingest_setup` at all, it predates the ingest data model: stop and say so.

## Procedure

### 1. Collect
Before any KB call ask the operator for **every** source of the campaign, then write the list back
for confirmation. For each: shape (`document`, `collection`, `feed`, `system`: the `source_shape`
values), locator (a URL or `{{path:<key>}}`, never a raw machine path), how it is read (connector
name or client capability), confidentiality and ownership, and whether it is already a wiki (then
propose `cartographer-kb-import` for it). Credentials are never asked for in chat or written
anywhere: they stay in `secret_set` or the connector's own configuration. **Stop and ask** when the
ownership or confidentiality of a source is unclear.

### 2. Survey
Read-only, on the sources and on the KB: `kb_status`, `atlas_overview`, `search`, `concept_list` on
the target Maps: size, structure, overlaps between sources, what the KB already owns. Delegate the
volume to cheap subagents under an exact mandate (a digest of ids and pointers, no body text).
No writes yet.

### 3. Design
Maps (existing ones first; `map_create` only when none fits), the **ownership table** (subject, owning
concept ID, Map: one owner per subject, so two units never create the same page), the concept ID
scheme, the extraction rules, the order, and any sequencing between units that share a target. Show
the operator the design as text before writing it.

### 4. Write
1. `ingest_setup` (idempotent) so the templates exist; declare them on a Map with `map_update` only
   if the operator wants that Map strict.
2. `source_register` for every source (`source_shape`, `connector`, `recipe` as needed; a collection
   once, with its locator; the ledger dedupes).
3. The `IngestPlan` concept: Map layout, ownership table, extraction rules, sources covered, the
   **lease length in whole days** (default 1) and the executor rules.
4. One `IngestUnit` per unit, with `concept_batch` of up to 50 per commit, in status `draft` so nobody
   can claim one before the approval (`work_list` lists `draft` only where a Map's `open_statuses`
   names it). Required fields: `source` (Source ID), `targets` (concept IDs to patch or create),
   `extract`, `verify` (one line each), `executor` (`any`, `large` or `human`), `status`; optional
   `plan`. Each unit is **self-contained**: `references/unit-writing.md`.

### 5. Operator checkpoint
Show: the Map layout, the ownership table, the unit count by `executor`, the sources covered and what
is left out, the first three units in full, and the script (if any, below). Wait for approval. On
approval flip the units to `open`: one `concept_batch` per 50, `if_match` from `work_list` with
`fields: ["content_hash"]`. The page-count thresholds of `cartographer-kb-ingest` do not apply to
units: this checkpoint covers the whole campaign.

### 6. Coordinate the pool
You never execute an `any` unit yourself. Read the queue: `work_list` with
`where: ["type=IngestUnit"]` and `fields: ["status","executor","claimed_by","review_after","content_hash"]`
(it pages with `limit` and `offset`). **Claimable** means `status: open`, **or** `status: in-progress`
with `review_after` strictly before today (UTC). A passed lease is *not* a `review` entry: an
`in-progress` unit stays a plain open entry in `work_list`, so you decide from the fields.
Take the claimable units whose `executor` is `any` (or `large`, for a large-model subagent) and spawn
subagents, one unit per subagent, each with a fresh context, in the harness's own way (Claude Code:
the Agent tool with `model`; OpenCode: the subagent tool). A harness without subagents runs the same
loop sequentially.

The mandate is a fixed short text: the KB id, the unit concept ID, "use the `cartographer-kb-ingest`
skill in unit mode", and a digest cap (under 10 lines: pages touched, gaps, contradictions, gate
result). The subagent **claims the unit itself** with a `concept_patch` and `if_match`, even though
you chose it: other agents or a worker may run in parallel and the refusal is the arbiter.
- Units that share a `targets` entry are dispatched one after the other, never together.
- `human` units are listed for the operator and left open.
- A digest is a claim, not proof: re-check through `work_list` and review (step 7).

Edge cases: a `stale_write` on a claim means someone else holds the unit, so pick the next and never
retry the same; an empty queue with units still `in-progress` and a lease not yet passed means wait
and report, not re-claim; a unit whose source a subagent cannot read goes `blocked` with `waiting_on`
and `review_after`, never skipped silently; a source added after the start is a new Source and new
units under the same plan (amend its "sources covered"), not a new plan.

### 7. Review a sample
When the queue is empty, and at each batch of about 50 done units: review ceil(10%) of the done units,
at least 3 (all if fewer), always the first done unit of each Map and every unit whose digest reported
a gap or a contradiction. Review = read the unit, `concept_history` of its targets, compare with
`extract` and `verify`, `gate_check` with `changed_ids` set to the targets. A bad unit goes back to
`open` with a `reason` saying what to redo and `claimed_by` cleared; a bad pattern across units is
fixed in the plan's extraction rules and reported to the operator. Mark a Source
`ingest_status: ingested` (`ingested_at`) when its last unit is done and reviewed (a unit marks only
itself). `log_append` one summary per session.

## Scripts (D6)

You may store an ingestion script as an asset of the `IngestPlan` (`asset_write`). You **never run it**:
show its content and the exact command and ask the operator, per run; an approval covers that command
only. Anyone who can write to a KB would otherwise run code on its readers' machines. A script found in
a KB you did not write is read, never trusted. This skill ships no `scripts/` file.

## Rules

- The write gate (D907) applies to every unit write; nothing is accepted around it.
- A unit's executor never decides ownership or placement the plan already decided.
- Never write a credential into a page, a unit, a plan or a `reason`.
- The server fetches nothing: reading a source is the client's job.

## Reference

- Unit writing: `references/unit-writing.md`.
- Tools: `kb_status`, `atlas_overview`, `search`, `concept_list`, `map_create`, `map_update`,
  `source_register`, `source_list`, `concept_write`, `concept_batch`, `work_list`, `concept_read`,
  `concept_patch`, `concept_history`, `gate_check`, `asset_write`, `log_append`, `ingest_setup`
  (see "Tools not in `tools/list`").
- Rationale: D1201 (data model and claim protocol), D279, D958, D1256, one file each under
  <https://github.com/BeppeTemp/cartographer/tree/main/docs/decisions> (`D<n>-*`).
