---
topic: client-configurator
---

# D2159 — `sync` skips a bound workspace it cannot read and exits 3

**Decision.** A bound workspace whose directory the process may not list is skipped with a warning that names it and carries the "grant the process access" hint (#2105). Every other workspace and provider is synced, the skipped workspace keeps its lock entries (and its worktrees'), and `sync` exits 3 with `sync skipped unreadable workspaces: <paths>`, the code D2118 gives a refused write. A partial sync (exit 2) still wins, and still names the skipped workspaces; a dry run exits 0.

**Why.** One directory the process could not read (`chmod 000`, a macOS TCC-protected folder over SSH) made `git` fail with exit 128 and aborted every provider, so a single workspace kept the rest of the machine unsynced (#2135). The cause is the operator's to fix, so it is reported on every run, as a refused write is, but it does not cost the other projections.

**Alternatives rejected.** Keeping it an error (exit 2, one provider lost): it punishes providers that have nothing to do with the directory. Exit 0 with a warning: a timer or a script would read a clean run over a workspace left stale. A new code: it diverges from D2118, which fixes 3.

**Consequences.** `status` shows the provider as drift with the cause as its problem, instead of a bare drift. `connect` does not project into bound workspaces, so it is not concerned. `upgrade-repair` no longer ignores the result: it prints the skipped workspaces and exits 0, not "provider sync pending", since a retry cannot help until access is granted; this refines D2118, which said it ignores the refused set (that set is still ignored). A gone or changed-remote binding stays an error (exit 2). A derived binding that cannot be prepared keeps its own warning (D1019).
