---
topic: client-configurator
---

# D1355 — `status` reports a managed bridge entry whose binary moved as drift

**Decision.** `status` compares each managed MCP bridge entry of a client with the command `bridgeCommand()` resolves now (`exec.LookPath` at the time `status` runs): an entry that names another binary, or a missing file, is listed as `diverged on disk (modified): mcp/<server>` (JSON `kind: "mcp"`) and makes `status` exit 1. Hand-written, HTTP and symlinked entries are not judged. `sync` and `upgrade-repair` already rewrite the entry, so nothing on the write side changes (#1355).

**Why.** Installing a release candidate in a directory ahead of Homebrew on `PATH`, then going back to the stable binary, leaves every client entry pointing at the old path; once it is removed the next client session starts without the server, and `status` said nothing. The cost: the verdict depends on the caller's `PATH`, so a TUI or hook with a different one can flag an entry a shell `sync` writes back.

**Alternatives rejected.** Rewriting the entry from `status`: `status` is read-only by contract. Recording the binary's path in the lockfile and comparing with that: it would still miss a binary that moved under the same lock. Leaving it to `reconnect`: a full disconnect and connect is far heavier than the fix.

**Consequences.** `diverged` may carry `kind: "mcp"`; a consumer of the JSON status must accept it. When no `cartographer` is on `PATH`, `status` reports no bridge drift although `sync` would write the HTTP fallback entry.
