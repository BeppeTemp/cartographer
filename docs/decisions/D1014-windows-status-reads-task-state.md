---
topic: control-plane
---

# D1014 — Windows `service status` reads the scheduled task state, not just its registration

**Decision.** On Windows `Status.Running` is true when `Get-ScheduledTask` succeeds and the task's `State` is neither `Disabled` nor `Ready`; the comparison runs inside PowerShell and only the token `stopped` or `live` crosses back. Any error or other output reads as running (fail open). `service status` therefore exits 3 after `stop` and `upgrade-repair` leaves a stopped service alone. The JSON field stays `running` and `lifecycle` keeps its three values.

**Why.** `Stop` stops and disables the task but leaves it registered (D156), so registration alone made a stopped service read as running, exit 0 instead of 3, and let `upgrade-repair` try to replace a service the user stopped. This is the Windows twin of D1006.

**Alternatives rejected.** `schtasks /query /v` "Status": localised, and `windowstask.go` never parses a localised string. `Get-ScheduledTaskInfo.LastTaskResult`: says nothing about now. Probing the port: not a registration or stop signal, and `Status` already reports health separately.

**Consequences.** `Ready` (enabled, no process: a crash, or the window between a drain and an explicit start) reads as stopped. A format change in the token cannot hide a live service. Unverified on a real host: whether a task disabled by hand while its process is alive reports `Running` in `State` (the macOS "live pid wins" rule); if it reports `Disabled`, `windowsTaskRunning` should be added as a second check.
