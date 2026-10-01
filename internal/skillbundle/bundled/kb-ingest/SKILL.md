---
name: kb-ingest
description: Ingest ONE primary source of any kind (document, meeting transcript or notes, chat thread, ticket or issue, incident timeline, email, web page, dataset) into an existing Cartographer KB - register it, merge its content into the pages that own the subject, record unknowns and contradictions, and leave a trace of which pages came from which source.
version: "1.0"
---
# KB Ingest — Skill

## Purpose

Absorb **one new primary source** into knowledge the KB already holds. Unlike `kb-import`, which
maps the structure of a whole foreign corpus with a mechanical scaffold, ingestion takes one
unstructured source and merges its **content** into existing pages. The agent reads the source;
the server never fetches or parses anything (D28, amended by D278). Binary formats (PDF, DOCX,
images) are read with the client's own capabilities. The procedure is the same for every kind of
source; kind-specific advice is in `references/source-kinds.md`.

Without this procedure the usual failures are: the same source ingested twice, new pages where a
patch belonged, existing claims silently overwritten, unknowns dropped, and no trace of which
pages came from which source.

## When to use

A new document, transcript, thread, ticket, incident timeline, email, web page or dataset must
become KB knowledge. Not for importing a whole wiki (`kb-import`), creating a KB (`kb-create`)
or server operations (`cartographer-ops`).

## Procedure

### 1. Identify
Kind, title, date, locator: a URL or a `{{path:<key>}}` placeholder, never a raw machine path.
When the source is a file, compute the sha256 of the **original bytes** with the client's shell
(e.g. `shasum -a 256 <file>`), lowercase hex.

### 2. Register
`source_register(title, source_kind, locator?, sha256?, timestamp?)`. The ledger journal
(`sources` by default) must exist: `map_create(name: "sources", kind: "journal")` if it does not.
- `duplicate: true` with `ingest_status: ingested` → **stop** and tell the operator.
- `duplicate: true` with `ingest_status: pending` → resume that Source (use the returned `id`).
- `source_list` shows what else is pending.

### 3. Distil
Write the distillation into the Source's body with `concept_patch`: key facts, decisions,
entities, dates, open questions. Every item carries a pointer back into the source (section,
timestamp, message, row) so a reader can verify it. For a long source, work section by section
and patch the body per section.

### 4. Place
For each fact find the page that owns its subject: `search`, `graph_context`, and `link_suggest`
for placement. Read it (`concept_read`); when its current content is surprising, `concept_history`
shows how it got there. Decide one of: **patch** the owning page, **create** a page (only when no
page owns the subject), open a **contradiction**, or **skip** (already known).

### 5. Checkpoint
If the plan touches **more than 5 pages** or creates **more than 2**, show the operator the change
plan first (pages to patch or create, gaps, contradictions) and wait for approval. Smaller
ingestions proceed directly. This threshold is the same for every client.

### 6. Write
- `concept_patch` / `concept_new` / `concept_write`, each with a `reason` naming the Source ID.
- Add the Source ID to the `provenance` of **every touched page**.
- A new page is also added to its map's `index.md` (`index_patch`).
- `concept_batch` when several pages change together (one commit).
- Credentials found in the source never go into a page: store them with `secret_set` (SOPS; see
  the `kb-create` skill's `references/secrets.md`) or leave them out and flag them to the operator.

### 7. Record unknowns
One `Contradiction` concept per real unknown, with `contradiction_kind: open_question` (raised,
not answered) or `missing_context` (the KB lacks something it should have), and `involves` naming
the pages concerned (D273). Gaps never block the gate.

### 8. Verify
`gate_check` (with `scope`) on the changed IDs. Fix what is yours to fix. Open contradictions are
left for the operator: they block the gate on purpose.

### 9. Close
- `concept_patch` the Source: `ingest_status: ingested` and `ingested_at` (RFC3339).
- `log_append` a one-paragraph summary.
- Report to the operator: pages touched, gaps opened, contradictions opened.

## Rules

- **Never ingest the same source twice**: registration decides, not memory.
- **One source per run.**
- **Patch over create**: a fact goes into the page that owns its subject.
- **Never delete or rewrite an existing claim because a source disagrees.** Open a
  `Contradiction` whose `contradiction_kind` is a real contradiction kind (not a gap kind), with
  `involves` listing both sides, and leave the page as it is. It blocks the gate until an operator
  resolves it (`conflict_resolve`).
- **Never write secrets** into a page, the Source body or a `reason`.
- **Stop and ask** when the source's ownership or confidentiality is unclear, before registering.

## Reference

- Tools: `source_register`, `source_list`, `search`, `graph_context`, `link_suggest`,
  `concept_read`, `concept_history`, `concept_patch`, `concept_new`, `concept_write`,
  `concept_batch`, `index_patch`, `gate_check`, `log_append`, `secret_set`, `conflict_resolve`.
- Per-kind guidance: `references/source-kinds.md`.
- Rationale: decisions D278 (source ledger), D272 (`reason`), D273 (gaps), D274
  (`concept_history`), D279 (this skill), found with `ls docs/decisions/D279-*`.
