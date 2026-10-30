# Operating loop

Cartographer provides deterministic building blocks; the agent decides how to
combine them for a task. There is no built-in LLM judge, review queue or
automatic PR workflow.

## Orient and read

1. Use `atlas_overview` or `map_list` to understand the KB shape.
2. Use `search` to find candidates.
3. Read only the relevant concept or section with `concept_read`. When a page's
   content surprises you, `concept_history` lists who changed it and why (the
   `Reason` of each write); `concept_read` with a commit `rev` shows an older
   version (D274).
4. Follow links with `graph_neighbors` when nearby context matters, or ask
   `graph_context` for the concepts most related to a question or to a few
   concepts, ranked, in one call.
5. After creating a concept, ask `link_suggest` which existing concepts it
   should probably link to.

Search results and indexes are derived. The Markdown concept remains the source
of truth.

## Write

1. Read the target first and retain its content hash.
2. When a suitable template exists, use `template_list` then `concept_new` to
   create its shaped starting point; otherwise use `concept_write` for a
   complete concept, `concept_patch` for bounded body/frontmatter edits, or
   `log_append` for a Journal entry. To curate a root or Map/Journal
   `index.md` — adding, removing or reordering entries — use `index_patch`
   instead of `concept_patch`: an expanded concept's own `index.md` (e.g.
   `map/concept`) is a concept and still goes through `concept_patch(id=
   <owner>)` (D122).
3. Pass `reason` (one sentence: the incident, source or decision behind the
   change) on any non-trivial write: it lands in the commit as a `Reason:`
   trailer and `changes_since` shows it (D272).
4. Pass `if_match` when updating existing content. A concurrent change fails
   with `stale_write` instead of being overwritten.
5. The server validates the write and, in a git KB, makes one local commit for
   the logical operation. The keyword indexes follow the files: the next search
   reconciles them (D245).

Remote synchronization is handled around writes when configured. See
[concurrency](concurrency.md) for freshness, asynchronous push and conflicts.

## Validate and lint

`validate`, `lint`, and `gate_check` are agent governance, not operator
maintenance: they are part of the core tool set precisely so a
descriptor-bound MCP host advertises them and this loop stays runnable
end-to-end (D123, → `control-plane.md` §MCP API).

- `validate` checks KB and frontmatter invariants.
- `lint([scope], [scope_neighbors], [check], [limit], [offset], [digest])` runs
  deterministic checks over a scope and, optionally, its graph neighbors,
  including any declarative map contract for required frontmatter and
  curated-index membership. `check`, `limit` and `offset` page the findings;
  `digest` folds them on a large KB.
- `gate_check(changed_ids, [scope])` combines the repository's
  deterministic validation checks; any lint finding (including missing required
  fields) makes it fail, because a finding is a problem and has no severity
  (D947). Pass `scope` to gate one prefix instead of the whole archive.
  `changed_ids` may be empty: the gate then
  runs validate and lint over the scope and skips the commit gate, which is the
  session-end check of the whole KB (D318).

Reasoning checks such as factual grounding, PII review or semantic
contradiction analysis are agent/human policy. Cartographer does not currently
run a second model or emit contradiction concepts automatically.

## Record unknowns as gaps

When writing, an unknown is recorded as a gap instead of being left as prose no
tool can find: a `Contradiction` concept with `contradiction_kind:
open_question` (a question raised, not answered) or `missing_context` (the KB
lacks information it should have) and `resolution_status: open`. A gap never
blocks a write, because writing is how it gets answered; `kb_status` lists open
gaps (`open_gaps`) and `work_list` lists them as work (reason `gap`). Once
answered, close it with `concept_patch`, setting `resolution_status: resolved`
and a `resolution` (D273). The advanced `contradiction_report` (`kind: gap`) and
`conflict_resolve`, callable by name, list and close them too.

A worker's synthesis task (D1263) also writes gaps, marked `origin: synthesis`, with the
evidence in `provenance`: they are hypotheses, not claims. Whoever confirms one writes the
content through the normal path, then closes the item with `resolution_status: resolved`, a
`resolution` and `resolution_outcome: confirmed`; rejecting it is the same with `rejected`
and the reason in `resolution`, so it is never proposed again.

## Register what you ingest

When the material comes from a primary source (document, transcript, ticket,
thread, web page), record it in the source ledger before writing pages from it:
`source_register` returns `duplicate: true` if the same source (same `sha256`,
else same `locator`) was already ingested, and `source_list` shows what is still
`pending`. Write the pages, cite the source by listing its ID in their
`provenance`, then mark it ingested with `concept_patch` (`ingest_status:
ingested`, `ingested_at`). `lint` flags an ingested source nobody cites
(`source_uncited`). The ledger is bookkeeping: the server never fetches or copies
the source (D278). The bundled `cartographer-kb-ingest` skill runs this whole step as one procedure — register,
distil, patch the owning pages, cite, record gaps, verify, close — for any new primary source (D279); a campaign of many sources is planned first with `cartographer-kb-ingest-plan` (D1256).

## The maintenance loop

The server finds the problems; an agent works on them only when the operator asks (D704), except a worker the operator has configured (D1258, [the worker](deployment.md#the-worker-a-third-role)). Discovery
is deterministic (`lint`, `kb_review`) and never runs a model on the server (D14). The layers,
cheapest first:

1. **Write-time repair** (D349, D355): a write applies the safe mechanical
   fixes to the concepts it just wrote, to a fixpoint, and the write gate
   (D350) refuses what introduces a finding the write could have avoided;
   both are fixed behaviour.
2. **Background repair** (D323): the server applies every safe fix over the KB, in one commit, with
   no agent: at every start (an upgrade or a config change can make untouched pages fixable), after
   a pull that moved HEAD, and once a day. The list and the period are fixed.
3. **Doctor session**: what needs judgement. The server proposes one: the first tool result of an
   MCP session that starts while debt exists and the last `kb-doctor` log entry is older than
   `kb.DoctorSuggestionDays` days carries a notice (only on a connection that declared `provisioned=1`, D1101) asking the agent to propose a `cartographer-kb-doctor` session
   to the operator. The agent never starts it without their agreement. In the session the
   operator decides each item (assisted) or hands it over ("do it yourself", delegated), up to
   10 review items (or the number the operator names when asking for the session) plus every mechanical repair; the session closes with a
   `log_append` listing every decision.
4. **What the KB does not know**: a fact nobody in the session can supply yet becomes a
   `Contradiction` of kind `missing_context` or `open_question` naming the concept and the
   question; the review item stays listed until the answer is written (nothing dismisses an item,
   D907), and the next session finds the gap instead of asking again. Ingest and any other session
   record gaps the same way. Gaps never block a gate (D273); `kb_status.open_gaps`
   and the Atlas Work panel (*to answer*) list them.

The Atlas keeps the two apart (D948). Its Health panel lists the lint's findings, which are
problems to fix (layers 1 and 2 repair the mechanical ones). Its Work panel lists what a person
answers or decides: layer 3's `kb_review` proposals, observations included (*to decide*);
layer 4's gaps, with the searches that found nothing (*to answer*); the open contradictions
(*to resolve*); and the pages past their `review_after` (*to review*).

## Compound useful results

When a result should survive the session, write it back as a focused concept
with links and provenance appropriate to that KB. This is a convention, not an
automatic post-processing step.
