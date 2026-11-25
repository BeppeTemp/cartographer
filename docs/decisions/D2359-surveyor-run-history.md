---
topic: control-plane
---

# D2359 — Surveyor runs are counted daily in a data-dir file

**Decision.** Every accepted Surveyor run is counted in daily counters per (UTC day, KB, perimeter, task, outcome), kept for 90 days in `<data>/.cartographer/surveyor-history.json` and served by `GET /api/ui/v1/kbs/{kb}/surveyor-history`. It is counters, not a log: no run id, replica id or error text leaves the store.

**Why.** The Atlas can show a 30-day trend of runs, failures and tokens only if something outlives the 20 runs per perimeter that Meridian keeps in memory and forgets on a restart. A KB write would be a commit and would feed the Surveyor's own activity signal, so the state lives in the server's own state directory, outside every KB. It costs one small file and a synchronous flush after each post that counted something (posts are at most one per beat).

- The day is the UTC day of the server's receipt, never the Surveyor's `started_at`: a skewed Surveyor clock cannot move a bucket (the rule the ages of the components route already follow). A run posted just after midnight lands on the new day.
- A run is counted once. A seen-set per principal, perimeter and replica (the last 64 run ids, with the KB) lives in the same file as the counters and is written atomically with them, so the re-post a Surveyor makes on `runs_wanted` after a restart does not double a count and a crash cannot count a run and forget it.
- The history is called from the runs route after the store's lock is released, counts what was accepted (not what the 20-run cap keeps), and can never change the answer: a failed write is logged once per distinct error and retried at the next change, memory stays authoritative.
- A file that is malformed or of an unknown version is renamed aside and the store starts empty; one that can be neither read nor moved aside is never written. The file is replaced atomically and never written through a symlink.
- Task and perimeter are free-form, so a day holds at most 250 distinct rows: a new key past it is dropped and logged once, existing rows keep counting, and no other day or KB is evicted by a flood.
- `first_day` is the oldest day the server holds counters of the queried KB. A caller who reads a KB whole thus learns since when the server counts: accepted, it reveals no other KB's name or count, and the route has the whole-visibility gate of the components route (`404` otherwise).

**Alternatives rejected.**

- Writing the counters into a KB: a commit per run, and it would feed the signal the Surveyor reads.
- Raising `maxRunsKept`: still memory, still lost on a restart, and the page would grow with the window.
- A field on the components route: that answer is polled and must stay small.
- The Surveyor's `started_at` as the day: a skewed clock would move buckets.
- A setting for the retention: a constant until an operator asks for another value.

**Consequences.** The history starts empty the first time a server with it runs; deleting the file resets the trend. Several servers keep separate histories, and a folder session keeps it in memory only (`persisted: false`). Counters of a KB later unmounted or renamed stay until the retention expires and are not shown; a KB mounted under that name later reads them (counts only).
