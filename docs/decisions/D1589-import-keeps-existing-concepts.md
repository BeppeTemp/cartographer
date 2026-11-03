---
topic: client-configurator
---

# D1589 — `import` keeps an existing concept unless `--overwrite`

**Decision.** `cartographer import` skips a source whose destination concept already exists in the KB, names it on stderr and counts it in `skipped`; `--overwrite` replaces it, and `--dry-run` opens the KB read-only to mark each collision. The automatic reindex that follows runs only for the imported KB, only when the local server mounts that directory and the client's server is loopback.

**Why.** An import is meant to be repeated as a source evolves, but the pages it writes are curated afterwards (frontmatter, body, `status`). Replacing by id turned a second import into silent data loss. The reindex used to be requested for every known KB of whatever server the client names, so an import into a clone nobody serves printed "search index rebuilt", and a remote server's KBs were reindexed with the operator's token as a side effect.

**Alternatives rejected.** Failing the import on the first collision (a rerun would never complete, and the exit code could not tell a collision from a write failure). Merging the source into the curated page (no rule that is right for frontmatter and body at once). Asking the server which directories it mounts (health lists names, not paths; the service config the same machine already holds is enough, and a remote server is excluded anyway).

**Consequences.** An existing concept counts as skipped, not as an error, so a rerun exits 0. The reindex target is derived from the service config (`data:` or a `kbs[]` path), so a KB mounted by other means gets the printed instruction. Replacing content stays possible, and explicit.
