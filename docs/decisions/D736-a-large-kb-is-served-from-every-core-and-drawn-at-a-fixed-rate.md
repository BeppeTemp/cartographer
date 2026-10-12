---
topic: control-plane
---

# D736 — A large KB is linted on every core, read once per request burst, and drawn at a fixed rate

**Decision.** The whole-KB lint runs its per-concept checks in parallel, one
worker per core, and the conformance cache computes each key once however many
requests ask for it at the same time (`singleflight`). The reads an Atlas page
repeats on every call are kept off the files: the concept panel's "used by"
artifacts are cached on the lint cache's key, the check catalogue reads the
types from the graph cache, `kb_status` reads every concept once and
reconciles the search index once per call rather than once per reported miss,
and `gitx` remembers the branch while `.git/HEAD` holds the same bytes. The
lint cache's stamp no longer counts the server's own state under
`.cartographer/` (search index, read and search-miss telemetry, repair log):
every `concept_read` and every search used to invalidate the whole-KB lint. In the browser the 3D
simulation ticks at a fixed 60 Hz, the warm-up that holds the first frame of
a new graph stops after 120 ms, and the graph no longer waits for the overview
to land.

**Why.** On a KB of about a thousand concepts the Atlas waited seconds for
work it repeated: switching KB fetched the overview, the artifacts, the lint
and the status together, and on a cold cache each one ran its own whole-KB
lint on a single core (2.3 s) while the graph waited for all of them; the
Health panel paid a git process per call and a whole-index reconcile per
reported search miss; a 120-144 Hz display ticked the simulation once per
frame, doubling its CPU and the pace of a drift tuned at 60 Hz. The cost is
concurrency in the lint: every per-concept check must now be free of shared
mutable state, which is what surfaced the unguarded `markerCache` (a crash
waiting for two concurrent lints, now behind a mutex).

**Alternatives rejected.**
- *Warm the caches in the background after each write*: the first reader
  would still wait whenever a write lands just before it, and the server
  would lint KBs nobody is looking at.
- *Run the simulation in a Web Worker*: it removes the main-thread cost but
  doubles the position traffic and reopens the one-writer rule of D234 for a
  gain the fixed rate already mostly gives.
- *Tick at 30 Hz once the graph rests*: the remaining idle cost is the
  many-body force, but the drift loses smoothness; proposed in the live
  session and declined.
- *Memoise the remote URL on `.git/config`'s stat*: `git remote get-url`
  also applies `insteadOf` rewrites from the global config and from included
  files, which no stat of `.git/config` sees; the process stays.
- *Cache `git log` by HEAD for Activity and Health*: `--since` filters on the
  committer date while the parsed commits carry the author date, so an
  in-memory filter would not answer the same question.

**Consequences.** A new per-concept lint check runs concurrently with the
others: it may read the run's shared data but must not write it (the target
body cache is the one shared map, behind its own mutex), and `go test -race
./internal/lint` is how to check it. Findings are produced in no particular
order, as before (they came from a map walk). A panic in a check is re-raised
on the request's goroutine, so it fails one request, not the process. A file
the lint starts reading under `.cartographer/` must be added back to the
stamp (`lintInputsStamp`). The branch memo is skipped when `.git` is a file
(worktree, submodule).
