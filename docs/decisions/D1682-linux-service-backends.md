---
topic: deployment-release
---

# D1682 — On Linux the service backend is chosen at runtime: systemd, else cron, else none

**Decision.** `cartographer service` on Linux picks its backend when it installs,
before writing anything: a `$HOME` that is not the account's home keeps writing
files only (#683); `systemctl` on PATH, `/run/systemd/system` a directory and
`systemctl --user show-environment` succeeding select systemd; `crontab` on PATH
and `crontab -l` answering (or "no crontab") select a cron backend; otherwise
there is none and `install` fails with exit 2 and nothing written. An installed
definition (the unit file, else `cron/server.cron`) wins over the probe for every
verb but `install`. The cron backend is a managed block in the user crontab with
an `@reboot` line and a 5-minute watchdog, both running `cartographer service
ensure`, which starts a detached `serve` unless one of ours runs, something
answers `/health`, or the stop marker exists. This amends D73 ("systemd only on
Linux").

**Why.** Slackware, Alpine/OpenRC, Void/runit, WSL without systemd and containers
have no systemd user manager, yet nearly all have `crontab`, which needs no
privileges. `/run/systemd/system` plus `show-environment` prove the user manager
is reachable (a `systemctl` binary left on a non-systemd host, a container or a
bare `su` fail one of them), so a unit is never written where it cannot run.
`@reboot` alone is not enough (busybox `crond` ignores it), so the watchdog is
the mechanism that works everywhere; it also heals a crash within 5 minutes. It
costs up to 5 minutes of downtime and a `crond` that must be running.

**Alternatives rejected.**
- Native OpenRC/runit/s6 backends: one implementation per init system, each
  needing root or its own conventions; cron covers them all.
- A resident self-daemonising `serve`: nothing restarts it after a reboot or a crash.
- `nohup` from `setup`: it does not survive a reboot and leaves nothing to inspect.
- Requiring systemd: leaves those hosts with no supported way to run the server.
- One shared block for server and sync timer: the sync timer runs on clients with
  no server, so each has its own block (the sync one arrives later).

**Consequences.** The stop marker survives reboots until `start`, `restart` or a
re-install. The server runs with a clean environment, so an operator's tokens
never reach it. Removal is verified as in #1584: the definition file is kept
while the block is still in the crontab. Paths with `%`, a newline or NUL are
refused. The probe never errors: a failing probe means "not this backend". The
sync-timer cron arm, setup and CI legs build on this record; the number is the
PR's.

**CI leg.** `test-linux-nosystemd` (`make test-linux`) runs `setup` and the
service verbs against a real cron daemon in Alpine (busybox `crond`) and Debian
(Vixie `cron`) containers with no init. It is a D1592 heavy leg: push to `main`,
`workflow_dispatch` and `merge_group`, never a PR, and watched by `main-status`.
