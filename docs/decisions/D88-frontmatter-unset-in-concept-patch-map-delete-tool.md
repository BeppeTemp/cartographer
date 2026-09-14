---
topic: control-plane
---

# D88 — Frontmatter unset in `concept_patch`, `map_delete` tool

**Status: implemented (2026-07-24).**

**Context.** `applyFrontmatterMap` (`internal/mcpserver/tools_write.go`) only ever called `fm.Set(...)`; passing a JSON `null` value for a frontmatter key set it to a literal `nil` rather than removing it, even though the removal primitive already existed (`Frontmatter.Delete`, `internal/okf/frontmatter.go`). The bundled `kb-import` skill prescribes removing `status: imported` once a concept is curated — doable before this change only via a full `concept_write` rewrite. Separately, `map_create` scaffolds a Map/Journal directory but there was no inverse: an emptied map (all concepts moved out) required an out-of-band `rmdir`.

**Decision.**
- **WP1 — null-as-unset.** In `applyFrontmatterMap`, a JSON `null` value now calls `fm.Delete(key)` instead of `fm.Set(key, nil)`. No new protection is added for reserved/required keys: the existing `kb.WriteConcept` validation (`type field is required`) already rejects a write that ends up without a `type`, so deleting it fails the same way a `concept_write` without `type` always has.
- **WP2 — `map_delete(map)`.** New tool, git-wrapped like every other write. Deletes the map/journal directory **only if empty** — i.e. it holds nothing beyond the `map_create` scaffold (`_map.md`, `index.md`, `log.md`); if any concept remains (found via `WalkConcepts` under the map's prefix), the map is left untouched and the error lists the concepts so the caller moves them with `concept_move` first. Classified agent-visible (not advanced), same tier as `map_create`.

**Rationale.** Reusing the existing `Frontmatter.Delete` and `WriteConcept` validation means null-as-unset needed no new protected-key logic — the "type is required" invariant already did the job. Emptiness-only `map_delete` (rather than a recursive/forced delete) keeps map removal a safe, git-diffable no-op unless the caller has actually finished migrating content out, consistent with `concept_delete`/`concept_move` never silently discarding concepts.
