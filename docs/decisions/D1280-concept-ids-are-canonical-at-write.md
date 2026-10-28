---
topic: control-plane
---

# D1280 — Concept IDs are canonical when they are written

**Decision.** A new concept must have a slug name (`a-z`, `0-9`, `-`, the rule `nonslug_file_name` repairs), may not differ only in case from an existing concept of its folder, and may not carry a NUL byte; the error names the canonical form. An existing page stays writable under its name, and a move or transfer keeps the source's own legacy stem. A `git_conflict_resolve` `edit` body is validated like a write (frontmatter with a `type`, no NUL, no conflict markers) before it is recorded. `concept_history` lists the commits of the concept's two forms and walks behind a `concept_move` only; it never infers a rename from similarity. `source_register` slugs fold accents and fall back to `source-<hash>`.

**Why.** A write that answered `findings: []` and was renamed later by the repair left callers holding an ID that changed; a case-only twin overwrote a file on a case-insensitive filesystem; `git log --follow` listed another concept's commit. The cost is that a caller who passed a loose name is now refused instead of silently fixed.

**Alternatives rejected.** Lower-casing and slugging the ID on write: the caller's ID would still differ from the one returned. Checking case only on case-insensitive filesystems: a KB checked out elsewhere would break. Keeping `--follow` with a higher similarity threshold: still a guess.

**Consequences.** The slug rule lives once, in `kb.Slugify` and `kb.SlugPattern`, used by the write path and the repair. A new write path that creates a concept goes through `prepareWriteConceptMode`.
