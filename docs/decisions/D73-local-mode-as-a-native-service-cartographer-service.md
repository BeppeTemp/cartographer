---
topic: deployment-release
---

# D73 — Local mode as a native service (`cartographer service`), Docker out of the local deploy

**Status: implemented (2026-07-09).**

**Decision.** The local deploy mode is the native binary daemonized as a **user
service** — LaunchAgent on macOS, systemd user unit on Linux — managed by the new subcommand
`cartographer service install|uninstall|start|stop|restart|status` (`internal/service` +
`cmd/cartographer/service.go`). `docker-compose.yml` is removed: the Docker image remains only
as a CI artifact for the k8s deploy, no longer a documented topology.

**Why.** (a) Containers on macOS are expensive exactly where Cartographer is most sensitive:
slow virtualized filesystem for git working trees and SQLite, always-resident VM, fragile bind
mounts — for a service that is a single static Go binary, the container added
nothing. (b) The "local Docker" topology was already just `serve --http --data` on a trusted network: the
native service is the same configuration without the layer in between. (c) A **single daemon**
per machine (instead of stdio spawned per-agent) preserves the single-owner invariant on the
working tree, per-KB locks (in-process mutex) and the SQLite index, and serves all the
configurator's providers (HTTP-only) without changing it.

**Multi-server cooperation (design clarification).** Multiple instances — local and/or k8s — mount
the same KB from the same git remote: git is the synchronization fabric (pull-rebase →
commit → push at every write), concurrent writes on the same concept degrade into
rebase-conflict/`needs-resolution` (expected behavior). Client-side constraint: one client
per machine, pointed at **one server at a time**.

**Attached choices.**
- `serve` over HTTP with `data:` configured but **empty** starts with 0 KBs (warning, `/health`
  active) instead of exiting: avoids the `KeepAlive` crash-loop on a fresh machine right
  after `service install`. Fail-fast remains for stdio and for no KB source configured.
- Default bind `127.0.0.1:8080`: loopback ⇒ auth auto-off with no network exposure.
- `service install` is idempotent and never touches an existing YAML config (`--data`/`--http`
  ignored with a warning: edit the file + `service restart`).
- `install.sh update` restarts the service **only if running** (`status` exit 0):
  a deliberately stopped service stays stopped, and `launchctl kickstart` on a job not
  loaded would fail anyway (systemctl-like exit codes: 0 running / 3 stopped / 4 absent).
- Sugar in `connect`: failed probe + loopback URL + service not active ⇒ offer of
  install+start with polling on `/health` (interactive) or a hint on stderr (non-interactive).
