---
topic: deployment-release
---

# D1479 — The Windows service detaches from its console, and `--log-file` captures stderr

**Decision.** `serve` and `sync`, started with `--log-file`, call `FreeConsole` at startup on Windows when `GetConsoleProcessList` reports this process as the only one attached to its console. `serve --log-file` also reassigns `os.Stderr` to the log file, not only the standard logger; stdout stays untouched because stdio transport carries the protocol on it.

**Why.** `cartographer.exe` is a console-subsystem binary, and the Scheduled Task of [D217](D217-the-native-service-on-windows-is-a-per-user-scheduled-task.md) runs it with an `InteractiveToken` principal in the user's desktop session, so Windows opened a visible console window for it — a Windows Terminal tab on Windows 11, which reads as a stray PowerShell. Closing it sent `CTRL_CLOSE_EVENT` and the service went offline. Lines `internal/mcpserver` writes straight to `os.Stderr` (timings, write-gate and search warnings) showed up in that window and never reached `server.log`; once the console is released they would go nowhere, so the redirect has to cover them.

"Only one attached" separates the task's fresh console from a terminal: a shell shares its console with the commands it starts, so `cartographer serve --log-file …` typed by hand keeps its window. A process with no console (stdio `serve` spawned by a desktop agent) makes the call fail and nothing changes.

**Alternatives rejected.** A second, GUI-subsystem binary (`-H windowsgui`) for the task: no window at all, but one more release artifact for goreleaser, `install.ps1` and upgrade-repair to keep in step. `conhost.exe --headless` as the task action: undocumented. An `S4U` logon type, which runs without a desktop session: needs the "Log on as a batch job" right a standard user usually lacks, and loses the Credential Manager git pushes rely on. `<Hidden>` in the task definition: it hides the task from the Task Scheduler UI, not the window.

**Consequences.** The console window still flashes for a moment when the task starts, before the process releases it. Diagnostics of the Windows service live only in the `server.log` and `sync.log` files under `%LOCALAPPDATA%\cartographer\Logs`. Code that captured `os.Stderr` in a package variable before startup keeps writing to the original handle.
