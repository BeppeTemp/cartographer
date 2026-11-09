---
topic: deployment-release
---

# D1862 — The tray follows the local service or, with none, the client's server

**Decision.** The tray picks its server on every tick. A local service installed (its definition exists) is shown exactly as before. With none, the tray shows the server of the client config (`server_url`), read-only. Never both at once. An explicit `--config` on `cartographer tray` forces the local mode. With no service and no client config file the tray behaves as D1482 describes (a server run by hand shows Running, nothing answering shows Not installed).

In client mode:
- `/health` of `server_url` is probed with the client's own rules (`client.CheckServerURL`, the `allow_insecure_http` opt-in, no forced `http://`, no loopback rewrite) and the client token, resolved as `sync` does;
- the token goes in the `Authorization` header of that one request and nowhere else, so the KBs and the update notice show;
- the menu has the status line naming the server host, Open Atlas on the server's Atlas URL, the KBs, the Agents rows and Quit; Restart Service and Open Log are not shown, and the tray stays read-only;
- `connect` installs the tray by default (`--no-tray` is remembered), the last `disconnect` removes it when no service is installed, and `service uninstall` keeps a tray a client still uses.

**Why.** D1482 tied the icon to the native service, so a client connected to a remote server saw "Not installed" although it had a perfectly good server. Choosing by what is installed keeps every existing machine unchanged and gives a client the same at-a-glance view without a second poll loop or a setting.

The probe is authenticated because an anonymous `/health` carries no `kbs` and no `latest_version` (D1096); the cost is a credential in a long-lived background process, kept to one header, never in a snapshot, a log line or an error.

**Alternatives rejected.**
- Showing both servers: two states in one icon with nothing to tell the user which one the shape means.
- An anonymous probe: only a status line, no KBs and no update notice.
- A setting to choose the mode: the machine already says which it is.
- A client-mode Restart or Open Log: there is no local service or log to act on, and the tray's read-only invariant (D1482) stays.

**Consequences.** `Open Atlas` is always offered in client mode: `/health` does not say whether the remote server serves the Atlas, so one without it answers 404 in the browser. `tray.OpenURL` accepts `https` and a `/ui/` path with a reverse-proxy prefix, still only a URL the tray built. The launchd plist and the Scheduled Task definition do not change: the mode is decided at run time. Amends D1482 and D1495.
