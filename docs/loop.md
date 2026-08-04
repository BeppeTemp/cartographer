# Operating loop

Cartographer provides deterministic building blocks; the agent decides how to
combine them for a task. There is no built-in LLM judge, review queue or
automatic PR workflow.

## Orient and read

1. Use `atlas_overview` or `map_list` to understand the KB shape.
2. Use `search` to find candidates.
3. Read only the relevant concept or section with `concept_read`.
4. Follow links with `graph_neighbors` when nearby context matters.

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
3. Pass `if_match` when updating existing content. A concurrent change fails
   with `stale_write` instead of being overwritten.
4. The server validates the write, updates live indexes and—when enabled—
   creates one local git commit for the logical operation.

Remote synchronization is handled around writes when configured. See
[concurrency](concurrency.md) for freshness, asynchronous push and conflicts.

## Validate and lint

- `validate` checks KB and frontmatter invariants.
- `lint(scope, scope_neighbors)` runs deterministic checks over a scope and,
  optionally, its graph neighbors, including any declarative map contract for
  required frontmatter and curated-index membership.
- `gate_check` combines the repository's deterministic validation checks; lint
  errors (including missing required fields) make it fail.

Reasoning checks such as factual grounding, PII review or semantic
contradiction analysis are agent/human policy. Cartographer does not currently
run a second model or emit contradiction concepts automatically.

## Compound useful results

When a result should survive the session, write it back as a focused concept
with links and provenance appropriate to that KB. This is a convention, not an
automatic post-processing step.
