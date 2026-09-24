---
topic: deployment-release
---

# D266 — A Windows restart waits for the old process to leave its port, and /health names the process

**Decision.** On Windows every path that ends the serve task — `restart`,
`uninstall`, the graceful replacement behind `restart --wait` / `upgrade-repair`,
and a re-install — goes through one stop that returns only when the old server
has exited: the task is no longer `Running` **and** nothing answers on the
config's http address. The shutdown event is tried first; if it cannot be opened
(the server runs in another logon session, or predates the event), or the drain
outlives its 20 s budget, `Stop-ScheduledTask` follows with its own 10 s wait,
and a fallback from the event is printed with its cause. Separately, `/health`
reports `started_at`, the RFC 3339 instant the answering process started, and
every post-restart check (`Replace`, `kb create|clone|rename --restart`) compares
it with the value read before the restart, so an answer from the old process
never counts as the new one.

**Why.** `Stop-ScheduledTask` returns before the process exits. The start that
followed reached a process still holding `127.0.0.1:39273`; the new instance
died on bind, then the old one exited, and every Windows restart left the server
stopped (#411). The health probe hid it, because it reached the old process
first. Uninstall had the other half of the same gap: unregistering a task does
not end its process (#413). And the event lives in `Local\`, which is per logon
session, so an SSH session cannot reach a desktop server's event (#415). The
cost is up to 30 s of waiting in the worst case, and a server that is killed
rather than drained when the event is out of reach — its in-flight requests
are cut and pending pushes are not flushed, which the notice says.

**Alternatives rejected.**

- *Wait on the task state alone.* It is what the drain already did, and the
  state is not proof that the socket is released: the bind error is about the
  port, so the port is what the wait checks.
- *Report the PID in `/health` and wait on the process handle.* It gives the
  same proof for the wait, but `/health` is unauthenticated and on a wildcard
  bind public; a start instant identifies the process for the before/after
  comparison without naming anything on the host, and needs no second
  PowerShell query.
- *Skip the start when the old process survives the stop.* A server that exits
  one poll after the deadline would stay down for good; the start is attempted
  (a no-op on a running task under `IgnoreNew`) and the survivor is reported.
- *Keep failing when the event cannot be opened (the previous behaviour).* It
  left `upgrade-repair` and an auto-patch apply from SSH with a replaced binary
  and the old process still serving it; stopping the task is what the operator
  would do by hand.
- *Keep the definition when uninstall cannot end the process.* The scheduler
  could not stop that process a second time either, so a retry would fail the
  same way; the definition goes, and the error names the process to end.

**Consequences.** `started_at` is additive and optional: a server that predates
it reports nothing, and every comparison then falls back to the check it had
before (version, or any healthy answer). A new path that stops the Windows
serve task must use `stopServeAndWait`, not a bare `Stop-ScheduledTask`.
