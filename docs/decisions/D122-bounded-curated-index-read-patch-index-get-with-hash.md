---
topic: control-plane
---

# D122 — Bounded curated-index read/patch: `index_get(with_hash)` + `index_patch`

**Status: implemented (2026-08-04).**

**Context.** `index_get` was read-only and returned raw Markdown without a
content-hash, so an agent that found an incomplete curated index (D107's
`require_index_entry`/`index_incomplete`) could diagnose the gap but not
repair it through MCP. `concept_write`/`concept_new`/`concept_patch` all
reach `WriteConcept`, which rejects the reserved `index.md` name for the
direct-concept path; `artifact_write` deliberately excludes `data/**` (D106).
An expanded concept's own `index.md` (e.g. `map/concept`) is different: it
*is* a concept and was already writable through `concept_patch`.

**Decision.** `internal/kb` gains a bounded pair, `IndexHash`/`PatchIndex`,
resolved through `curatedIndexRelPath`: it accepts only the root `index.md`
and an existing Map/Journal's `index.md` (a real `_map.md`/`_archive.md`
descriptor must exist), rejects traversal, nested paths, non-index reserved
files and symlink components, and redirects a two-segment expanded-concept
index path to `concept_patch(id=<owner>)` with a typed `expanded_index`
error instead of silently misclassifying it. `PatchIndex` writes the
already-materialized replacement content atomically after an immediate
content-hash comparison against `ifMatch`, failing closed with
`ErrStaleWrite` and leaving the file untouched on any rejection.

At the MCP layer, `index_get` gained an opt-in `with_hash: true` returning
structured `{path, content, content_hash}`; the default stays byte-for-byte
raw Markdown for every existing caller. `index_patch` reuses `concept_patch`'s
Edit-tool semantics verbatim (single `old_string`/`new_string`/`replace_all`
or an atomic `edits` batch) applied to the curated index's full content
instead of a concept body, goes through `gitWrap` (one commit, one root
`log.md` entry), and never touches the live/SQLite concept search indexes —
curated indexes are not concepts. Authorization introduces a fifth resource
class, *curated index*: a Map/Journal path is authorized against that single
collection, the same as `concept_move`'s destination; the root index has no
Map/Journal of its own, so it requires the same unscoped whole-KB write grant
as the tools in that class, even under a policy that already grants a write
inside one specific Map.

**Rationale.** Bounded explicit curation was chosen over automatic index
generation: an index is deliberately curated prose (ordering, grouping,
prose framing an agent chooses), not a derived listing — `concept_list`
already covers the exhaustive/derived case. Reusing `concept_patch`'s exact
Edit-tool semantics and JSON shape keeps one mental model for "patch some
text under `if_match`" instead of inventing a second patch grammar for
indexes. A new resource class (rather than folding `index_patch` into
`resourceWhole` like `index_get`) lets a Map-scoped principal curate its own
Map's index without a whole-KB grant, while keeping the root index — which
has no bounded collection of its own — behind the same unscoped grant every
other whole-KB tool already requires.
