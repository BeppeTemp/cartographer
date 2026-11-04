---
topic: control-plane
---

# D1640 — The search index deletes FTS rows by rowid and excerpts the final hits only

**Decision.** `internal/sqlindex` keeps a side table `fts_rows(id, kind, fts_rowid)`
(schema version 5) with the rowids of each concept's rows in `concepts_fts` and
`sections_fts`; an update or delete removes them with `DELETE ... WHERE rowid = ?`.
A search pages `(id, score)` only and computes `snippet()` afterwards for the
final hits, in chunks of 200 ids.

**Why.** FTS5 cannot index an `UNINDEXED` column, so `DELETE ... WHERE id = ?`
scanned the table on every update (quadratic on a reconcile), and `snippet()` in
the paged SELECT was evaluated for every row of every page, hidden hits included
(quadratic when `allow` hides many). It costs one more table to keep in step with
the two FTS tables and one extra query per search.

**Alternatives rejected.**
- Align `concepts_fts.rowid` with `concepts.rowid`: `concepts` has a text primary
  key, so its rowid is not stable across `VACUUM`, and `sections_fts` has many rows per id anyway.
- A `fts_rowid` column on `concepts`: needs a table migration, and sections still need a map.
- Keep `snippet()` in the page and only shrink the first page: does not fix the quadratic case.

**Consequences.** Every insert into an FTS table must record its rowid and every
delete must go through `deleteFTSRows`. The index is a derived cache: opening a
version-4 database drops both FTS tables and `fts_rows`, empties `concepts`, and
the next reconciliation reindexes once.
