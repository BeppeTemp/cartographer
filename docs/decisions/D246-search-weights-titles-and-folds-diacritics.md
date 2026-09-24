---
topic: control-plane
---

# D246 — Search weights titles and folds diacritics

**Decision.** Both search backends weight a concept's title 5, its frontmatter
2 and its body 1. Both also fold diacritics, so "attivita" finds "attività". In
SQLite, the FTS5 table has three indexed columns (`title`, `meta`, `body`)
ranked by `bm25(concepts_fts, 0, 5, 2, 1)`, with the tokenizer
`trigram remove_diacritics 1`. The in-memory index applies the same weights to
its token counts and folds tokens with a stdlib table. The SQLite layout is
versioned through `PRAGMA user_version`: an older database is dropped and
rebuilt on open.

**Why.** A concept named after the query used to rank with every page that
mentions it in passing, and on Italian KBs an unaccented query missed accented
text. The rebuild costs one full reindex on the first start after the upgrade.
The index is disposable, and the D245 reconciliation already knows how to fill
an empty table.

**Alternatives rejected.**
- *`golang.org/x/text` normalization for folding*: a new dependency for what a
  table of about 150 letters does. NFD decomposition also changes rune counts,
  and the snippet relies on folding being one rune for one rune.
- *Folding ß, æ, œ to "ss", "ae", "oe"*: the fold would stop being one rune for
  one rune, so an offset found in the folded text would no longer be valid in
  the original. These letters are left alone.
- *Migrating the v1 table in place*: FTS5 cannot add columns, and re-splitting
  every row is what a reindex does anyway.
- *Exact top-k parity between the backends*: trigram substring matching and
  whole-word tokens legitimately differ (D89). Parity is required only where it
  matters, which is a query equal to a title ranking that concept first.

**Consequences.** bm25 weights are positional over every column, the unindexed
`id` included, so the query's leading 0 must stay (commented at `ftsRank`).
`snippet()` reads the body column. For a title-only match it returns the start
of the body, and an empty body falls back to the in-memory excerpt. The next
change to the FTS layout bumps `schemaVersion`. Removing a document from the
in-memory index now touches only that document's own terms.
