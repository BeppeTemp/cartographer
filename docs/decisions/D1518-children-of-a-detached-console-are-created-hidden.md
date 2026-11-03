---
topic: deployment-release
---

# D1518 — Children of a process that detached its console are created hidden

**Decision.** Every `exec.Cmd` reachable from `serve`, `sync` and `tray` goes through `procattr.Hide` (`internal/procattr`): on Windows it sets `HideWindow` and `CREATE_NO_WINDOW`, elsewhere it does nothing. A parse-only guard test, `TestEveryChildProcessIsHidden`, fails when a non-test file starts a process without it, unless the file is on the test's allowlist.

**Why.** [D1479](D1479-the-windows-service-detaches-from-its-console.md) releases the task's console with `FreeConsole` so no window can be closed under the service. A console-subsystem child started by a process with no console gets a new, visible console unless it is created with `CREATE_NO_WINDOW`, so the fix of D1479 turned every PowerShell, `taskkill`, `git` and `sops` start into a window. Setting the flag by hand at each of about forty call sites is what drifts; a guard that lists the bare calls keeps it from drifting back.

**Alternatives rejected.** Hiding only the sites seen opening a window: the next `exec.Command` is the next window. A GUI-subsystem binary for the service: one more release artifact, rejected already in D1479. Re-attaching the process to a hidden console of its own: undocumented behaviour, and the children would still share it.

**Consequences.** A new `exec.Command` in the code path of the service or the tray calls `Hide`, or its file joins the allowlist with the reason (Unix-only, a GUI-subsystem binary, a tool that never runs as a service). A hidden child has no console, so a tool that needs one to read from (`sops` prompting) must read from a pipe, as the callers already do.
