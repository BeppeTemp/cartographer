---
topic: client-configurator
---

# D1269 — A Claude Code config or settings file is deleted at `{}` only if Cartographer created it

**Context.** D63 kept `.claude.json` forever ("never deleted"), so an empty-HOME
`connect`→`disconnect` left a `{}` behind (#1269). The maintainer decided a file Cartographer
created and that is empty after removing our entries is deleted; one that existed before or
still holds other keys is kept.

**Decision.** Replaces D63 point 3. `configurator.Apply`, `registerMCPServer` and
`registerHookSettings` record in `<baseDir>/.cartographer-created.json` (a ledger of relative
paths, read and written through `internal/safewrite`) every Claude Code config/settings file
they create. `configurator.Remove`, `removeMCPServer` and `removeHookEntries` delete such a file
once it is `{}` and drop its entry; the ledger goes with its last entry. A file absent from the
ledger is reduced and kept, even as `{}`. kiro/opencode stay deletable by content (their files
hold only MCP entries, D63). The workspace exclusion list covers the ledger. Dry run decides on
the same record.

**Discarded alternatives.** Content-only deletion (`{}` means delete): it deletes a user's own
pre-existing `{}` file. A new field in the lockfile or client state: not reachable from
`internal/configurator`.
Details: `docs/configurator.md` §`cartographer disconnect`.
