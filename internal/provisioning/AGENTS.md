# AGENTS.md — provisioning and the client side

This package writes into **other people's home directories**. That is the whole
reason it has its own rules: a defect here does not fail a test, it silently
modifies files a user did not ask about.

## What you must not get wrong

- **Never write to a path without `Lstat`ing every component first.**
  `os.WriteFile` on a symlinked path opens the *target* with `O_WRONLY|O_TRUNC`:
  it does not replace the link. This is not hypothetical — one `cartographer sync`
  modified **23 files** inside an unrelated git checkout, each stamped with a
  provenance footer naming Cartographer as their source (D148). The KB side had
  always guarded this; the client side had 21 unguarded calls.
- **A hand-curated user file is never rewritten, only its managed block is.**
  `internal/blocktext` does marker-delimited substring replacement and nothing
  format-aware, on purpose: comments, ordering and every other byte survive
  (D58). If you find yourself parsing the user's TOML or JSON to edit it, stop.
- **Merging into a client's MCP config is non-destructive**: entries belonging to
  other servers are preserved (D23). A full rewrite of the file is a bug even when
  the result looks right on your machine.
- **A provider's capabilities are declared, not inferred.** `registry.go` holds one
  descriptor per provider and the unsupported cells are explicit with a stated
  reason, so `Unsupported` means "no approval unblocks this" (D50). Do not make a
  destination up for a provider that does not document one.
- **Prune is as dangerous as write.** It removes what the lockfile says we own;
  anything outside it is someone else's. Deleting an empty directory has
  boundaries for the same reason (D63).
- The lockfile is the client-side record of applied state. Changing its shape is a
  migration, and an empty new field must keep meaning what the files written
  before it meant (see `base_dir`, D141).

## Scope note

The repository's own `.claude/skills` and `.kiro/skills` are symlinks into
`.agents/skills` (D203). Because of D148 this package will refuse to materialize
into them, which is the correct outcome: those are repo-scoped skills, versioned
here, and not something a KB sync should own.
