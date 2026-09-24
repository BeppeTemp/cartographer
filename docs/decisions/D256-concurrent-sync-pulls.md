---
topic: sync-provisioning
---

# D256 — The client pulls the KBs' manifests concurrently, and reports errors in KB order

**Decision.** `fetchCandidates` issues the per-KB `sync_pull` calls concurrently, at most four in
flight (`maxConcurrentPulls`). Each call only fetches, decodes and hash-checks its own KB's
artifacts; everything that depends on order runs afterwards over the results in target order: the
error that is reported (the first failing KB in that order, not the first to fail in time) and the
cross-KB signature check over `seen`.

**Why.** Each `sync_pull` can wait on a server-side fetch of its KB's remote (D93), and the server
serializes git work per KB only, so in series a sync cost the sum of those waits (about 22 s for
five KBs, where the slowest one took about 7 s). Concurrently it costs about the slowest one. Keeping
the order-sensitive steps sequential keeps the result and every message identical to the
sequential loop, so a failing run names the same KB on every attempt. What it costs: a run that
fails on its first KB still waits for the others' responses, and a server sees up to four fetches
from one client at once.

**Alternatives rejected.**
- One goroutine per KB with no bound: a large setup would start a fetch against every remote at
  once, for no gain once the slowest KB dominates.
- Returning the first error in time and cancelling the rest: the reported KB would change between
  runs, and an operator retrying would chase a different message each time.
- `golang.org/x/sync/errgroup`: it cancels on the first error, which is the behaviour above; a
  `sync.WaitGroup` with a channel semaphore needs no new direct dependency.

**Consequences.** `pullTarget` must stay free of state shared between targets; the per-target
`client.MCPClient` is a copy (`WithKB`), which is what makes this safe. A future "continue past a
KB that cannot be pulled" (#350) can read the per-KB results directly instead of stopping at the
first error.
