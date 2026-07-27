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

The bundled `kb-import` skill contains the operator procedure.

## Share one KB with different agent clients

Run the HTTP server, then use `cartographer connect` for Claude Code, Codex,
Kiro or OpenCode. The client installs one MCP entry per KB and materializes
the KB's managed skills, agents, hooks and instructions. `cartographer status`
shows drift; `cartographer sync` reconciles it.

Providers differ in what they can represent. See [configurator](configurator.md)
and [synchronization](sync.md) for the exact mappings and limitations.

## Separate domains into multiple KBs

Mount independent git repositories under one HTTP server and give tokens
`kb:<name>:r` or `kb:<name>:rw` scopes. A client connects to a specific KB
through its query/path route, and each KB has independent indexes, commits,
sync state and conflict registry.

Authorization is per KB, not per concept. Use separate KBs when data needs
different access boundaries.
