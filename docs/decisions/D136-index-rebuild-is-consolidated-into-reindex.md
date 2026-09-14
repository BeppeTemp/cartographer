---
topic: control-plane
---

# D136 — `index_rebuild` is consolidated into `reindex`

**Decision.** `index_rebuild` is removed and its behaviour becomes `reindex(full: true)`:
`full: false` (the default, and a call with no arguments) keeps today's incremental
reconciliation and its `indexed`/`updated`/`removed` counters; `full: true` rebuilds the
in-memory index from every concept and repopulates SQLite FTS5, returning `status:
"rebuilt"`, `concepts_indexed`, and `sql_upserted` only when a SQLite index is present. The
merged tool is **write-scoped**: it writes server-owned state, so a token holding only
`kb:<name>:r` can no longer trigger a rebuild. `full: true` works without a SQLite index —
that was `index_rebuild`'s only unique capability — while the incremental mode keeps
returning "SQLite index is unavailable", since there is no persisted state to reconcile
against. `cartographer reindex` gains `--full`; in its administrative fallback (server down)
there is no live in-memory index to rebuild, so the flag reports that it is reconciling the
persisted index instead, which already covers every file.

**Rationale.** Once embeddings were gone (D135) the two tools differed only in how much they
rebuilt, while carrying opposite rationales for the same side effect: `index_rebuild` was
classified read-only because the index is derived and gitignored, `reindex` write-scoped
because it writes the server's SQLite database. Both statements described the same write.
Nothing in either name or description told an agent which one to call — and the answer
depended on a deployment detail (whether SQLite was available) it could not see. One tool
with a thoroughness switch removes the choice; resolving the classification toward
write-scoped is the honest reading, and a read-only client rewriting the server's index was
never intended.
