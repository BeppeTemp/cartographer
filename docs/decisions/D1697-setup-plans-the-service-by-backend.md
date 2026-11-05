---
topic: deployment-release
---

# D1697 — setup plans the service by backend and keeps a server that already answers

**Decision.** `setup` reads the backend of D1682 and the answer of `/health`
into its facts and decides the server step in this order: installed and running
is kept; installed is started; a well-formed `/health` answer on the configured
address (the default one when there is no config) is **kept** with no installed
service, flagged unmanaged; no backend is a plan error (exit 2, nothing created,
the manual route printed); otherwise install. The plan line names the backend
(`cron`: starts at boot, checked every 5 minutes). The failure hint and the
linger note name only what the backend has: `journalctl` and `loginctl` are
systemd's, cron points at `server.log` and `supervisor.log`. A kept unmanaged
server cannot be restarted by setup, so after a KB create or clone setup stops
with exit 1 asking for a restart by hand. `service install --config-only` writes
the config and the data directory exactly as `install` does, installs nothing and
exits 0: without it the manual `serve --config` points at a file nothing creates.
`connect` does not offer the install where there is no backend. The sync timer
gets its own cron block (`sync`, never `@reboot`): the interval is rounded up to
whole minutes, from an hour up to whole hours, capped at daily, because cron has
no "every n minutes after the last run".

**Why.** The original failure (#1648) was a setup that wrote a config and a data
directory and then died on `systemctl`. A server the operator already runs by
hand is the supported answer on a host with no service manager, so setup must
not try to replace it, and `/health` must be well formed so an unrelated process
on the port is not taken for one.

**Alternatives rejected.** Installing over an answering server (a second
instance, or a bind failure that hides the real state); having setup restart a
hand-started server (it cannot know how it was started); one block for server
and sync timer (the timer runs where there is no server).

**Consequences.** The number is provisional (the PR's number replaces it); it
adds to D1682 and amends nothing else.
