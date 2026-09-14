---
topic: client-configurator
---

# D63 — Complete prune: empty directories with boundaries, MCP configs reduced to empty (WP7)

**Context.** A `connect`→`disconnect` round trip did not return the filesystem to its initial
state: `PruneManaged` ran `os.Remove` on the individual `ManagedFile` entries but never on the
containing directories left empty; `configurator.Remove` left `{"mcpServers": {}}` on disk once
the only entry was removed.

**Decision.**
1. **Empty directories (`pruneEmptyDirs`).** After removing a managed file, it walks up the
   parent directories deleting them if empty, always stopping at a known root
   (`.claude`, `.codex`, `.kiro`, `.opencode`, `.config`, `.config/opencode`, or `BaseDir`).
   `os.Remove` (never `os.RemoveAll`) fails on its own on a non-empty directory, so a user
   file/other artifact stops the ascent without an explicit check.
2. **MCP configs reduced to empty.** If the server map ends up empty, the key is deleted; for
   files dedicated solely to MCP config (kiro, opencode) the whole file is deleted if nothing
   else remains.
3. **Absolute invariant: `.claude.json` is never deleted** (shared state broader than
   Claude Code) — it stays reduced to `{}`, a residue accepted by construction.
4. **End-to-end test** (`TestRoundTrip_ConnectDisconnect_NessunResiduo`) verifies, with a
   `filepath.Walk` before/after, that the only difference is the set of exceptions above and that
   pre-existing user files survive.
**Discarded alternatives.** `os.RemoveAll` on the providers' directories (it would delete content not
managed by Cartographer); always deleting the MCP config file when the entry is removed (wrong
for `.claude.json`, broader shared state).
Details: `docs/configurator.md` §`cartographer disconnect`.
