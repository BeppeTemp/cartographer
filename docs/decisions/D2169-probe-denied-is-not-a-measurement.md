---
topic: deployment-release
---

# D2169 — a connect the OS refused is a skipped check, never "nothing listens"

**Decision.** `ProbeListener` returns `ListenerDenied` when the connect fails with `EPERM`, `EACCES` or `WSAEACCES`, and `ProbeHealth` returns an error `service.ProbeDenied` recognizes. `service status` skips the check with the reason `connection_denied` (`health_checked: false`, never `healthy: false`), and no caller that would act on a missing server acts on a denied probe: `connect` offers no install, `upgrade-repair` and `Manager.Replace` restart nothing, `setup` stops before changing anything, `doctor` reports the server as not checked, and the cron start and the Windows install refuse as for a busy address.

**Why.** In a sandbox without network access (Codex `workspace-write`, a macOS seatbelt profile) `connect()` to loopback fails with `EPERM`, which `ProbeListener` read as `ECONNREFUSED`: a healthy server showed as down, and the agent restarted it and the user rebooted for a fault that did not exist (#2165). The same rule as D174 holds: a check that could not run is not rendered as a verdict. The cost is one more state every `ProbeListener` switch has to name, and a sandboxed `setup` or `upgrade-repair` now stops instead of trying.

**Alternatives rejected.**
- Treat a denied probe as `ListenerForeign`: wrong cause, and it would send the operator to free a port nothing holds.
- Fold it into `ListenerUnresponsive`: that state means a peer accepted the connection, which is the opposite of what happened.
- Retry or fall back to another probe (a unix socket, a file): the sandbox that denies the connect gives no truthful answer by another route either.

**Consequences.** A new caller of `ProbeListener` or `ProbeHealth` that turns "none" or an error into an action (install, start, restart, replace) must handle `ListenerDenied` / `ProbeDenied` first. The tray has its own probe and is out of this rule. `docs/configurator.md` (`service status`) and `docs/deployment.md` state the current behaviour.
