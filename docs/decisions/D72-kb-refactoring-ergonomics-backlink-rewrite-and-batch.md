---
topic: data-plane
---

# D72 — KB refactoring ergonomics: backlink rewrite and batch in `concept_move`, consistent index, inventory, explicit dossiers

**Status: implemented (2026-07-09), all WPs.** Delta versus the plan below:
(a) WP1 — *write* errors during the backlink rewrite abort the pass and are
propagated as a tool error (only the index upserts stay best-effort); a single
summary `AppendLog` for the whole batch call (not one per rewritten concept); move
chains within the same batch (`a→b` + `b→c`) are not supported: validation reads the
sources from disk before applying; (b) WP1 — new primitive `kb.RewriteLinks(body,
basePath, moveMap)` alongside `ExtractLinks`, same resolution for both syntaxes;
(c) WP4 — the "new dossier" check happens with `os.Stat` before the `MkdirAll`, so a
pre-existing dossier is never touched; the `dossier_missing_index` lint is directory-based
(new dedicated scope-match helper). The `homelab-wiki` remediation (stub index.md in the 7
implicit dossiers + `index_rebuild`) is an operational post-deploy step, not part of the code.

**Context.** Real session (2026-07-09): restructuring of the `homelab-wiki` KB — 47
`concept_move` calls to introduce the dossier level in `entities/` and `topics/`. Total cost
~1M tokens, largely attributable to control-plane gaps, not to the agent:
1. `concept_move` does not rewrite backlinks → a subagent fixed ~269 `[[...]]` links by
   hand (592k tokens, 368 tool calls, 50 min) for work the server can do in one commit;
2. `concept_move` does not update the indexes (unlike write/patch/delete): stale FTS
   entries on the old IDs still present 8 days later, duplicated results in `search`;
3. no inventory tool: ~30 `search` calls with trick queries ("a e i o u", "di") to
   enumerate the concepts, with entries still missed on the first pass;
4. 47 moves = 47 commits and 47 locks, not atomic (interruption halfway = mixed KB with broken links);
5. moves to nested paths created 7 **implicit** dossiers (side effect of the `MkdirAll`
   in `WriteConcept`) without the `index.md` that `dossier_create` would have generated → `index_get`
   fails on those paths; moreover `PathToID` accepts arbitrary depth, while the data
   model (`data-plane.md` §Hierarchy) allows at most archive/dossier/concept;
6. (surfaced during planning) `ExtractLinks` (`internal/kb/graph.go:14`) parses **only**
   markdown links `[testo](path.md)`, never the wiki-links `[[id]]` — which are the de facto
   syntax used throughout the `homelab-wiki` KB. `graph_neighbors`, the lint's `broken_link` check and
   orphan detection are therefore blind to the real links.

**Decision.** Structural refactoring is a first-class control-plane operation:
the server already has all the data (files, links, indexes) and must absorb its mechanical cost.

**WP0 — First-class wiki-links in `ExtractLinks`.** Precondition of WP1: `ExtractLinks`
also recognizes `[[id]]` and `[[id#sezione]]` alongside markdown links. Semantics: wiki-links
are **root-relative** (the ID is the path from the KB root without `.md`, as in real usage:
`[[entities/smart-home/otbr]]`), while markdown links remain relative to `basePath`. No
alias form `[[id|testo]]` (added only if needed — **post-implementation note**: the
first full lint on `homelab-wiki` revealed that the alias form IS used in some concepts;
those links work for the reader but are invisible to graph/lint/backlink-rewrite —
a natural candidate for a future WP: parse + rewrite of the alias form). Intended side
effect: `graph_neighbors`, `lint broken_link` and orphan detection start seeing
the real links — after the fix a `lint full` on `homelab-wiki` will give the true picture.

**WP1 — Batch `concept_move` with backlink rewrite.**
- New parameter `moves: [{source_id, target_id}]` (backward compatible: single `source_id`/`target_id`
  remain valid as a batch of 1). Full validation of all entries **before**
  applying (sources exist, targets free, path guard); then application and **a single
  commit** for the whole batch.
- Server-side backlink rewrite: a single `WalkConcepts` pass with the complete
  old→new map, rewriting both syntaxes (root-relative wiki-links `[[old-id]]`/`[[old-id#sezione]]`
  and markdown links resolved via `ExtractLinks`) in all concepts, including
  `services/`. Parameter `rewrite_links` (default `true`). The result lists the applied
  moves and the concepts touched by the rewrite.
- The rewrite goes through the same write path as concepts (hashes, indexes) — no raw writes.

**WP2 — Index consistency on move.** `toolConceptMove` receives `live` + `sqlIdx` like
`concept_delete`: removal of the old ID's entry and upsert of the new one for every move and for every
concept rewritten by the backlink rewrite. A post-move `search` must not return dead IDs.

**WP3 — Inventory: `concept_list([scope], [limit])`.** Read-only tool that enumerates concepts
(`id`, `title`, `type`) under a prefix, via `WalkConcepts` + frontmatter — the bounded
equivalent of "ls -R". Registered agent-visible: it is the natural first step of any
reconnaissance and replaces the abuse of `search` as an enumerator. `index_get` remains the curated
route (progressive disclosure), `concept_list` the exhaustive one.

**WP4 — Explicit dossiers and enforced depth.**
- When a write (`concept_write`/`concept_move`) implicitly creates a new
  dossier directory, the server also generates the `index.md` stub (`type: Index`, title from the name)
  — same content as `CreateDossier`. `index_get` must never fail on a real dossier.
- Enforcement of the documented hierarchy on the write path: ConceptID in `data/` with **max 3
  segments** (archive/dossier/concept); explicit error beyond that. `services/` unchanged (2
  segments). `lint` check: dossier without `index.md` (for KBs born before the fix).
- **`homelab-wiki` remediation** at activation: `index.md` in the 7 implicit dossiers
  (`entities/{infra,ai-tools,smart-home,clients}`, `topics/{smart-home-protocols,infra,ai-tools}`)
  and `index_rebuild` for the stale FTS entries.

**WP5 — Documentation and closure.** `control-plane.md` §MCP API (batch `concept_move` +
`rewrite_links`, `concept_list`), `data-plane.md` (enforced depth, implicit dossiers),
`readonly.go`/`visibility.go` + golden tests, `server_test.go` (atomic batch, rewrite with
anchors, stale index, depth guard, index.md stub), this entry, and the release
tracking state then in use.

**Order.** WP0 and WP2 are small and autonomous (can start immediately, even in parallel);
WP1 depends on WP0 and is best done after WP2 in the same area; WP3 and WP4 independent; WP5
closes. All additive and backward compatible (WP0 widens graph/lint, no API change).
