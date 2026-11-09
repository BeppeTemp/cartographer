---
topic: control-plane
---

# D704 — An agent works on a KB only when the operator asks

> **Amended by D1258**: a process started by `cartographer worker` with a config the operator wrote is the operator asking; nothing else changes.

**Decision.** Cartographer never starts a `kb-doctor` session by itself. The scheduled headless
doctor (D369, D690) is removed, and `connect`, `sync` and `upgrade-repair` delete the job 0.22
and 0.23 installed (a Homebrew upgrade runs only the scheduled `sync`). The nudge always asks the agent to *propose* a session (the old `assisted` mode);
`doctor_mode` is still validated but ignored, so the `unattended` default of D358 is gone;
`kb_status` reports the capability as `assisted`, because a skill synced before 0.24 reads a
missing one as `unattended`. The
server's mechanical repair runs once at every start, as well as after a pull and on its interval,
so a fix does not wait a day in a queue. The Atlas Health panel follows: it shows what was found,
by kind, including the `kb_review` proposals a session decides (a read-only
`/maintenance/review` endpoint), and on a second tab what Cartographer checks; it no longer shows
lanes, a schedule, the fixes already made or a next-step prompt.

**Why.** An agent working on a KB unasked spends model quota and changes content nobody chose to
change at that moment. Discovery is already deterministic (lint and `kb_review`, D298): the
operator decides when an agent acts on it. A mechanical fix needs no decision, so it should not
look like pending work.

**Alternatives rejected.**
- Keep the schedule as an opt-in: it is still an agent started by a timer, and supporting it
  costs three scheduler formats, a server declaration route and a client token in the job's
  environment.
- Remove the nudge entirely: a KB nobody looks at would never mention its debt. A proposal costs
  one line in a session the operator already opened.
- Keep the startup delay computed from the last run: an upgrade that adds a check would leave its
  fixes waiting up to one interval.
- Health as the doctor's flow (checks → fixed → doctor → you): with no agent acting by itself
  there is no flow to show, and a counter of questions for the operator is empty by construction
  when the operator is in the session. Counting "decisions" instead of kinds read as a promise of
  how much work a session is.
- The fixes already made on the page: they need nothing from the reader; the commits stay in the
  KB's history and `repair_revert` undoes them.

**Consequences.** A KB nobody opens an agent on accumulates review items until someone asks for a
session. The doctor budget default is the assisted one (10). The token file of D699 stays: the
sync timer still reads it. Supersedes the unattended default of D358 and D369/D690.
