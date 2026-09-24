---
topic: control-plane
---

# D245 — Search indexes follow the files

**Decision.** The in-memory and SQLite search indexes are kept fresh by
validation, the same way as the link graph (D241). Before every search, after
every git pull and on `reindex`, the graph cache reports every concept whose
resolved content changed, appeared or vanished since the previous
reconciliation (`kb.ConceptChanges`). One per-KB reconciler applies that delta
to both indexes. No write path updates an index any more.

**Why.** Every write handler used to notify the indexes itself, and some
forgot: `conflict_resolve` and `git_conflict_resolve` never did, a server
without SQLite never saw a git pull until it restarted, and an operator's
editor was seen only after a manual `reindex`. Validation covers all of these
with one mechanism, with or without git and with or without SQLite. It costs one
stat walk per search, a few milliseconds and no file reads when nothing
changed.

**Alternatives rejected.**
- *Keep notifying, and fix the handlers that forget*: it fixes today's list, and
  the next handler that writes a file would forget again. D243 had just added
  the same kind of fix to `supersede`.
- *A hook in `kb.WriteConcept`*: it does not see every change. Moves and
  collapses change files with `os.Rename`/`os.Remove`, and pulls and editors
  never call it.
- *A hook on `gitWrap`'s commit*: git may be disabled, and `CommitOp` returns
  early when auto-commit is off.
- *Validate after every write*: that is a stat walk per write that no reader
  asked for. Validating on the read path costs a walk only when someone
  searches.

**Consequences.** `reindex` without `full` works without SQLite and is the same
reconciliation a search runs. `concept_batch` no longer has an index step
inside its rollback boundary, because the files are the only state to restore.
The cache still holds no bodies: a change carries the content the validation
just read, and a second read happens only for a file the validation reused. The
offline `ReconcileIndex` stays for the CLI and the startup check, which also
drops SQLite rows for concepts removed while the server was down.
`TestIndexUpdatesOnlyInReconciler` fails if any other code in
`internal/mcpserver` updates an index.
