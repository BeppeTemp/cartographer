---
topic: control-plane
---

# D1258 — The configured worker is the asking

**Decision.** D704 (an agent works on a KB only when the operator asks) is superseded only for
a process started by `cartographer worker` with a config the operator wrote and deployed for a
perimeter: deploying it is the asking, as much as typing a prompt. The worker is a third role of
the single binary, a process of its own that `serve` never starts or links. One config describes one
perimeter (source credentials, model endpoints, budgets); replicas are more processes with the same
config. Model profiles are named and chosen per task, with a budget per profile (a list of
profiles, `tasks` an ordered list: priority is order). The key table in `internal/workerconfig` is the
one place that lists known keys. A run's spending is rebuilt from the KBs' logs, never from local disk.

**Why.** What D704 removed the timer for (spending quota unasked, changing content nobody chose to
change) is what a deployed worker is for, and the operator chose the budgets, window, KBs, model
profiles and tasks. Every write carries a `Reason` trailer and is revertible. The price is that a
worker spends without a human at the keyboard, bounded only by the budgets and the token's scopes.

**What does not change.** The server runs no model and never starts a session (D14, D704). The client
installs no scheduler (D369's job stays removed); `connect`, `sync` and `upgrade-repair` still delete
it. The nudge still only proposes. `doctor_mode` stays ignored (D358 stays superseded): a worker that
runs the doctor is the operator's configured work, not the `unattended` default.

**Alternatives rejected.**
- Restoring D369's client-side timer: three scheduler formats and a token in a job's environment, the costs D704 cited.
- An opt-in flag in the server config: the server would start agent work, against D14.
- The worker as a mode of `serve`: one process would hold both the KBs' credentials and an inference key.
- Wrapping a headless client CLI: it needs the CLI and its subscription auth in the image, with weaker tool control.
- One inference endpoint and one budget per worker: a local model would share the cloud's ceiling.
- In-process sub-agents: their state would not survive a restart nor span replicas; the queue is the sub-agent mechanism.
- A name-keyed `models` map: `internal/settingschema` documents fixed keys and lists of mappings, not the entries of a map.

**Consequences.** Nothing is written to local disk, so a replica needs no volume. What survives a
restart:

| State | Where | After a restart |
|---|---|---|
| Spend of finished runs, per profile | the closing `log_append` entries in the KBs | rebuilt before each run |
| Spend of the run in progress | in-process counters | lost on a crash: a day can exceed a profile's `daily_tokens` by at most its `run_tokens` per crash |
| A closing entry that could not be appended | nowhere | counts like a crash for that run and KB |
| Work half-done | the KB (a claim with a lease, commits already made) | an expired lease is re-claimed by any replica; partial commits are ordinary revertible commits |
| Window, idle state, poll timers | recomputed from the clock and the server | nothing to lose |
| Replica identity | memory | a new id; the old claims lapse by lease |
| Replicas' counters | not shared | two replicas starting together both see the same `remaining`: overshoot is bounded by `replicas x run_tokens` per profile |

The budget is a cost-control guard, not a quota the provider enforces. A future change must keep the
worker free of `internal/mcpserver` and `internal/kb`, `serve` free of the worker, and the file free of secrets.
