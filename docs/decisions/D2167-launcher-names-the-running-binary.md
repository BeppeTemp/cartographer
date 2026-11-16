---
topic: sync-provisioning
---

# D2167 — every client entry names the binary that runs the sync, else the one on PATH

**Decision.** One resolver (`bridgeCommand`) gives the command of every entry a client starts: the bridge, the `mcp exec` launcher, the Cline dispatchers, the drift check and the test for whether an entry can be written. It is the absolute path of the running binary when that path outlives the run, and the `cartographer` found on PATH otherwise. A path does not outlive the run when it is under a versioned Cellar directory, under a temporary directory (`os.TempDir()`, `/tmp`, `/private/tmp`, `/var/tmp`) or in a directory anybody can write to without the sticky bit.

**Why.** With a release candidate beside a package manager's stable one, the first `cartographer` on PATH is not the binary that wrote the entry, and a client started the other one, which has no `mcp exec` and printed its usage (#2152). The launcher alone naming the writer would leave the bridge entries and the drift check on another command, so one sync could write two. The cost is that an entry follows the binary that last synced: running an older binary by path rewrites the entries to it, and the drift check then calls the previous command stale.

**Alternatives rejected.** Changing only the launcher: providers would disagree, and the drift check would flag the bridge entries on every sync. Always `os.Executable()`: under Homebrew it is a versioned Cellar path the next upgrade deletes (D737). Always PATH, as before: it is the bug.

**Consequences.** Residual cases are not detected: a `/nix/store` path (immutable, dropped by a garbage collection) and a Windows 8.3 or differently cased spelling of a temporary directory; both would be written as they are. A new reader of "the command a client starts" goes through `bridgeCommand`, never `exec.LookPath` or `os.Executable` directly. `TestBridgeCommandNamesTheWritingBinary` pins the rule.
