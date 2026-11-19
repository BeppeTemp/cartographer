---
topic: deployment-release
---

# D2281 — The perimeter is a Surveyor's identity for orphaned claims, and a poll that finds nothing writes nothing

**Decision.** A unit's `claimed_by` is the replica id, the perimeter name plus a random suffix made at each start. At its first run on a KB, a Surveyor releases the claims whose `claimed_by` is its own perimeter's name and another suffix, with a note, and a unit released (or found orphaned) while a newer unit of its `observed_group` exists leaves the queue as `skipped` with `superseded_by`. A system poll that finds nothing to do writes nothing, not even the lease or `feed_checked_at`; the lease is taken when the observation finds work.

**Why.** The suffix changes on every restart, so a claim of the previous process could only lapse with its day-granular lease and the unit sat unworked for up to two days. The perimeter is what the operator configures, and one perimeter runs one process at a time. A poll that only moved `feed_checked_at` committed to the KB at every interval for no news, and a failure already recorded did the same.

**Alternatives rejected.** Persisting the replica id on local disk: the Surveyor writes nothing locally, and a lost volume would orphan every claim again. A shorter lease: `review_after` is a date, so a day is its floor. Keeping the lease write and skipping only the advance: the lease alone is a commit per interval. Writing a heartbeat on the claim: a commit per beat.

**Consequences.** Two processes of one perimeter running together (a misdeployment) release each other's claims at start; the claim's compare-and-swap still keeps one winner per unit, so the cost is a repeated run, not a double write. The schedule of a quiet poll is in memory, so a restart polls once more. A system poll's loser now lists before it loses unless the Source's hash already changed, which the poll checks first.

**The author.** Every client the Surveyor builds sends `Cartographer-Author: surveyor/<perimeter>/<replica> <surveyor@surveyor.invalid>`, the form the Atlas reads as a Surveyor's change; without it the server falls back to the token's name. The email is fixed, not built from the perimeter name: a perimeter name need not be a valid email local part, the server's check wants a plain `local@domain`, and `.invalid` (RFC 2606) can never receive mail.

**The `Starter` interface.** An instance that has work to do once per KB per process implements `Start(Runtime)`; the runner calls it for each KB the first time it works that KB, before the first task, with no model and no grant. The ingest task uses it for the orphan sweep, which has to run even when no unit is claimable (a claim that has not lapsed is not offered by `Next`, so `Run` would never be reached). A sweep whose listing failed is not marked done and is tried again at the next run.
