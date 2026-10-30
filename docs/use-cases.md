# Practical use cases

These examples use the current MCP surface. They are patterns an agent can
follow, not server-side automations.

## Maintain a runbook during an incident

1. Find the affected service with `search`.
2. Read the relevant section through `concept_read`.
3. Patch the verified recovery step with `concept_patch` and its `if_match`
   hash.
4. Add a timestamped incident note with `log_append(path=...)`.
5. Run `lint` on the runbook scope and review the git commit.

Cartographer does not infer the root cause or open a review PR. The agent and
operator own those decisions.

## Keep operational procedures current

Use `changes_since` to see what changed recently, then update a procedure with
`concept_patch` or replace it with `concept_write`. `supersede` can mark one
concept as replaced by another; git retains the full history. `if_match`
prevents an older session from overwriting a newer revision.

## Import an existing notes directory

`cartographer import` creates resumable `status: imported` drafts. Work through
them incrementally:

1. inventory with `concept_list`;
2. search/read a bounded group;
3. curate types, titles, links and provenance;
4. clear the imported status when a concept is reviewed;
5. run `validate` and `lint`.

The bundled `cartographer-kb-import` skill contains the operator procedure.

## Ingest one new source into existing knowledge

A document, meeting transcript, thread, ticket, incident timeline, email, web page or dataset
arrives and must become KB knowledge. The bundled `cartographer-kb-ingest` skill (D279) is the procedure: the
agent registers the source in the ledger (`source_register`, which refuses a second ingestion of
the same source), distils it, patches the pages that own each subject instead of creating
duplicates, cites the source in their `provenance`, opens a `Contradiction` rather than overwriting
a claim the source disputes, records unknowns as gaps, and closes with `gate_check` and
`log_append`. The agent writes, then reports the pages, the commits and how to revert them (every write is a
revertible commit); it asks first only when the operator asks for a preview. On a strict map that does not
list `Contradiction`, an unknown becomes an open checkbox on the owning page, reported for promotion.
The agent reads the source; the server never does.

## Ingest a large corpus as a campaign

Hundreds of sources (a documentation site, a ticket archive, a set of feeds) are not ingested
one at a time by hand. The bundled `cartographer-kb-ingest-plan` skill (D1256) is run by a large model with the
operator: it collects the sources, designs the Maps and the subject-ownership table, writes an `IngestPlan`
and one self-contained `IngestUnit` per source, and waits for approval. Small or local models then execute
the units one each with `cartographer-kb-ingest` in unit mode, claiming them from `work_list` with an
`if_match` patch, while the planner reviews a sample of the done units. When the corpus is already a wiki to
be mapped wholesale, use `cartographer-kb-import` instead.

## Follow a live source

A wiki space, a tracker project or a document library should flow into the KB as it changes. The operator
starts the bundled `cartographer-kb-source-author` skill (D1373): it finds the way in (a built-in recipe, an exec
lister or an MCP connector), requires a read-only identity, writes the lister and the worker config snippet into the
operator's repository, and runs `cartographer worker source test`, which lists with the real recipe and writes
nothing. It then registers the Source, hands it to `cartographer-kb-ingest-plan` for the plan, and hands the
operator the deploy change. The script never enters the KB.

## Keep an old KB aligned with the current standard

A KB whose pages predate the conventions, or were written by agents that never saw them, drifts: synonym
fields (`updated` for `timestamp`), tool parameters stored as fields, links to retired pages.
`kb_status` reports it as `conformance` and sets `doctor_suggested`; the bundled `cartographer-kb-doctor` skill
then runs `kb_repair` as a dry run per check, applies it after the operator confirms (one commit per
check, however many concepts), and walks the judgement fixes with the agent. Nothing runs by itself.

## Share one KB with different agent clients

Run the HTTP server, then use `cartographer connect` for any supported client
(the matrix is in the README and [synchronization](sync.md)). The client installs
one MCP entry for the server (`/mcp/routed`, which serves every KB it mounts) and
materializes the KB's managed skills, agents, hooks and instructions. `cartographer status`
shows drift; `cartographer sync` reconciles it.

Providers differ in what they can represent. See [configurator](configurator.md)
and [synchronization](sync.md) for the exact mappings and limitations. A
descriptor-bound host such as Codex can only call tools `tools/list`
advertises; the core set (→ `control-plane.md` §MCP
API) includes `validate`, `lint`, `gate_check`, and `kb_status` for exactly
this reason (D123).

## Separate domains into multiple KBs

Mount independent git repositories under one HTTP server and give tokens
`kb:<name>:r` or `kb:<name>:rw` scopes. A client's entry is the routed
mount (`/mcp/routed`), narrowed to the KBs it should reach with `?kbs=`, and each
KB has independent indexes, commits, sync state and conflict registry.

Authorization is per KB, not per concept. Use separate KBs when data needs
different access boundaries.
