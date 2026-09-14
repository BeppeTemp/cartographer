---
topic: control-plane
---

# D70 — Incremental update ergonomics: `concept_patch` + snippets in `search` results

**Status: active.** Implemented as planned (WP1–WP3 below): pure string-replace on the body
only (no section-aware), title/snippet in all search modes (in-memory, FTS5, hybrid).

**Context.** Analysis of a real session (2026-07-07, Sonnet 5, wiki closure after an
HA "bedroom fan" change) showed two control-plane frictions:
1. to add a few lines to a concept the agent had to re-read and rewrite the entire
   body (~9k chars) via `concept_write` — and before the write it *instinctively* attempted an
   old_string/new_string `Edit` call on a nonexistent local copy of the concept: models
   expect a patch-style affordance that is missing today. The full rewrite costs tokens and risks
   silent loss of sections through hallucination;
2. `search` returns only `{id, score}` (`searchHit` in `tools_search.go`): every hit forces
   a full `concept_read` to figure out whether it is relevant.

**WP1 — `concept_patch` tool.** New tool in `tools_write.go`, semantics identical to the clients'
`Edit` tool: `{id, old_string, new_string, replace_all?, if_match}` with `if_match`
**mandatory** (a patch only makes sense on an existing, already-read concept). Handler:
`ReadConcept` → check `if_match` (`stale_write` error like `concept_write`) → replacement
on the **body only** (dedicated errors: `old_string_not_found`, `old_string_ambiguous` on
multiple matches without `replace_all`) → reuse of `concept_write`'s write path (frontmatter unchanged
except an optional `frontmatter` parameter with shallow-merge for bumping `aggiornato`/`fonti`;
liveindex, `sqlIdx.Upsert`, git commit via gitwrap): extract a shared helper instead of
duplicating. Register in `RegisterKBTools` and classify **rw** in `readonly.go`. Note: the
`okf.SectionHashes` already exist — a future section-aware patch is possible, but the first
iteration stays pure string-replace (simpler and matching the expected affordance).

**WP2 — `search` with `title` + `snippet`.** Extend `searchHit` with `Title` and `Snippet`
(~200 chars) in all modes (in-memory keyword, FTS5, hybrid). FTS5: native auxiliary
`snippet()` function in `SearchFTS` (`internal/sqlindex`). In-memory/semantic: excerpt around
the first occurrence of the term (fallback: first lines of the body); title from the frontmatter —
if the liveindex does not hold it already, add it there, **no** per-hit `ReadConcept` inside the
search handler. Response budget: with `limit` 20 and 200-char snippets the payload stays <5k chars.

**WP3 — Documentation and closure.** `docs/control-plane.md` §MCP API (new tool + new
search fields), this entry (from PLANNED to active), and the release tracking
state then in use. Tests in
`server_test.go`: patch happy-path, `stale_write`, `old_string_not_found`/ambiguous,
`replace_all`; snippets in `internal/sqlindex` and in the in-memory fallback.

**Companion action (outside this repo, to do at activation).** Update the curated
`instructions.md` of the `homelab-wiki` KB (via `concept_write`): a "KB-first" rule
mirroring the closure rule — before exploring HA/cluster for an already known entity,
`search` the KB and put the concept's digest into the explorer's mandate. In the analyzed session the
KB already contained the device's details but was consulted only in writing, after the work was done.

**Order.** WP1 and WP2 independent (parallelizable); WP3 closes. No migration: both
are additive and backward compatible (clients using only `concept_write` do not change).
