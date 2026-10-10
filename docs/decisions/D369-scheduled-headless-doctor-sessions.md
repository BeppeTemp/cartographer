---
topic: client-configurator
---

# D369 — A scheduled headless doctor is a client-side opt-in, declared to the server

**Decision.** `cartographer doctor schedule` installs a daily native per-user job (launchd agent,
systemd user timer, Scheduled Task) that runs `cartographer doctor run`, which starts one agent
client headless with the `kb-doctor` skill in unattended mode (D358) and a 2-hour limit.
`unschedule` removes exactly those files, `status` reads them back. It is never done by `connect`,
`setup` or `sync`. The client declares `{client, next_run}` to the server (`/api/doctor-schedule`,
stored in `.cartographer/doctor-schedule.json`) at scheduling time and after each successful run;
the Atlas Health panel then says "Next doctor session: ..." instead of "Starts when an agent next
connects", and a declaration more than a day past its `next_run` is ignored.

**Why.** A KB nobody opens an agent on never receives the nudge, so its doctor debt never
converges. The server has no model (D14) and must not get one; the client already has one, and the
operator's own scheduler recipe (D358) was the only way to use it. A scheduled run spends model
quota unwatched, so it is explicit, reversible and bounded rather than a default.

**Alternatives rejected.**
- An option inside `connect`: the sync timer is installed by `connect` because a hook-less client
  needs it to work at all (D325); a quota-spending job is not needed for anything to work, so it
  must be a deliberate command.
- The scheduler running the client directly: three unit formats would each carry the client's
  headless flags, the timeout and the declaration. A `doctor run` subcommand holds them once.
- Re-declaring on every run attempt: a client that fails daily would keep Health promising sessions
  that produce nothing. Only an exit 0 re-declares, so failure lets the declaration go stale.
- An MCP tool for the declaration: it is client-to-server metadata, like usage reports (D326), not
  an agent operation, and a tool would spend entry budget on the whole catalogue (D285).
- Reading the schedule back from the OS scheduler on the server: the server runs elsewhere
  (a pod, another host) and cannot see the client's scheduler.
- Permission-bypassing flags by default (`--dangerously-skip-permissions` and the like): that is a
  trust decision for the operator. `--client-flag` passes whatever the client needs, explicitly.
- One schedule per KB or per client: one machine-wide schedule keeps `status` and `unschedule`
  unambiguous; scheduling again replaces it.

**Consequences.** Hermes is out until a non-interactive mode is documented in `harnesses.md`. A
new client enters `headlessClients` only with that documentation. The job needs the token variable
of an authenticated server in its environment, as the sync timer does; without it the run still
happens and only the declaration fails (a warning). The declaration is a hint, never trusted
beyond display: bounded in time, validated, and ignored when stale.
