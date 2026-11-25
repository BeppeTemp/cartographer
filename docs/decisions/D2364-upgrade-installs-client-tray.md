---
topic: deployment-release
amends: D1862
---

# D2364 — upgrade-repair installs the client's status icon once

**Decision.** On macOS and Windows with no local service installed, `upgrade-repair` installs the status icon once when the client has a connected agent, no remembered `tray_opt_out`, no tray installed and a stable binary path, and prints `status icon installed: …`. A failure is a warning and never changes the exit code. An installed tray is refreshed as before; a remembered opt-out is always respected; Linux and a machine with a local service are unchanged.

**Why.** D1862 promised a connected client the icon, but only `connect` installs it, so a client connected before that release never had one until the user ran `cartographer service tray install` after reading the `status` hint. The upgrade is the moment such a machine gets the release.

**Alternatives rejected.**
- Leaving it to the `status` hint: the icon D1862 promises stays missing for everyone who does not read it.
- Installing on every `upgrade-repair`: an opt-out is remembered, and a tray the user removed by hand with no opt-out is rare; "once" is enforced by the installed and opt-out checks.

**Consequences.** D1862's "`upgrade-repair` never creates one" no longer holds for a connected client. `sync` still installs nothing.
