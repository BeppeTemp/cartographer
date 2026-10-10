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
5. The server validates the write, updates live indexes and—when enabled—
   creates one local git commit for the logical operation.

Remote synchronization is handled around writes when configured. See
[concurrency](concurrency.md) for freshness, asynchronous push and conflicts.

## Validate and lint

`validate`, `lint`, and `gate_check` are agent governance, not operator
maintenance: they are part of the default `agent` tool profile precisely so a
descriptor-bound MCP host advertises them and this loop stays runnable
end-to-end (D123, → `control-plane.md` §MCP API).

- `validate` checks KB and frontmatter invariants.
- `lint(scope, scope_neighbors)` runs deterministic checks over a scope and,
  optionally, its graph neighbors, including any declarative map contract for
  required frontmatter and curated-index membership.
- `gate_check(changed_ids, [severity_min], [scope])` combines the repository's
  deterministic validation checks; lint errors (including missing required
  fields) make it fail. By default it reports lint findings from `warning` up:
  the `info` checks cannot fail a gate, so they are not in the way of reading
  the ones that can (D186). Pass `severity_min: "info"` to see them, and `scope`
  to gate one prefix instead of the whole archive — the verdict is computed on
  the unfiltered results either way. `changed_ids` may be empty: the gate then
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
gaps (`open_gaps`) and `contradiction_report` with `kind: gap` lists them all.
Once answered, close it with `conflict_resolve` (D273).

## Register what you ingest

When the material comes from a primary source (document, transcript, ticket,
thread, web page), record it in the source ledger before writing pages from it:
`source_register` returns `duplicate: true` if the same source (same `sha256`,
else same `locator`) was already ingested, and `source_list` shows what is still
`pending`. Write the pages, cite the source by listing its ID in their
`provenance`, then mark it ingested with `concept_patch` (`ingest_status:
ingested`, `ingested_at`). `lint` flags an ingested source nobody cites
(`source_uncited`). The ledger is bookkeeping: the server never fetches or copies
the source (D278). The bundled `kb-ingest` skill runs this whole step as one procedure — register,
distil, patch the owning pages, cite, record gaps, verify, close — for any new primary source (D279).

## The maintenance loop

A KB maintains itself, transparently to the people who read it (D358). People see
the result and an audit trail; they are asked only what the KB cannot know. Four
layers, cheapest first, none of which runs a model on the server (D14):

1. **Write-time repair** (D349, D355): a write applies the mechanical
   `auto_repair` fixes to the concepts it just wrote, to a fixpoint, and the
   write gate (D350) refuses what introduces findings.
2. **Background repair** (D323): once per `doctor_auto_interval` the server
   applies `auto_repair` over the KB, in one commit, with no agent.
3. **Unattended doctor**: the server cannot decide, so it nudges. With
   `doctor_mode: unattended` (the default) the first tool result of each MCP
   session that starts while debt exists and the last `kb-doctor` log entry is
   older than `doctor_interval` (1 day) carries a notice telling the agent to run
   the `kb-doctor` skill now. The agent decides the items under the skill's
   rules (it may merge a subset duplicate, place a section, write a template,
   fill a section from cited sources; it never deletes a concept, renames a map
   folder or invents content), up to `doctor_budget` review items (40) plus every
   mechanical repair, then closes with a `log_append` listing every decision and
   gap. A KB with more debt than one budget simply gets the next session at the
   next nudge. `doctor_mode: assisted` restores the previous behavior: a notice
   per 14 days asking the agent to propose a session to the operator.
4. **Gaps for people**: an item only a person can settle becomes a
   `Contradiction` of kind `missing_context` or `open_question` naming the
   concept and the question, and the review item is dismissed with `lint_ignore`
   citing the gap. Gaps never block a gate (D273); `kb_status.open_gaps` and the
   Atlas Health panel are the queue people answer.

### A KB no agent session ever touches

The nudge needs a client session to land in. For a KB nobody reads through an
agent, schedule a headless agent session instead (D369):

```
cartographer doctor schedule --client <client> --kb kb-a --at 06:00
```

This is opt-in and never done by `connect`: it installs a native per-user daily job (launchd,
systemd user timer or Task Scheduler) that starts the chosen client non-interactively with the
prompt `Run the kb-doctor skill on kb "kb-a" unattended.`, spending that client's model quota
without anyone watching. `cartographer doctor status` shows it and `cartographer doctor
unschedule` removes it entirely; flags, clients and details are in `configurator.md`
§Scheduled headless sessions. The client declares the schedule to the server, so the Atlas Health
panel says "Next doctor session: tomorrow · 06:00" instead of "Starts when an agent next
connects"; a declaration more than a day past its run is dropped and the old text returns. The
server still has no model (D14): it only knows when the client says the next session is due.

The fallback, for a client `doctor schedule` does not cover or a scheduler of your own, is the same
job by hand, with the client's own non-interactive flag in place of `<headless-flag>`:

```
0 6 * * *  cd $HOME/work && <client> <headless-flag> 'Run the kb-doctor skill on kb "kb-a" unattended.'
```

Either way the session is an ordinary one, so the nudge, the budget, the closing log entry
and the gaps work exactly as above.

## Compound useful results

When a result should survive the session, write it back as a focused concept
with links and provenance appropriate to that KB. This is a convention, not an
automatic post-processing step.
