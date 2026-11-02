---
topic: data-plane
---

# D1201 — Ingest pipeline data model

**Decision.** An ingest campaign is two conventional, keyed concept types: `IngestPlan` (one per campaign) and `IngestUnit` (one per unit of work), with templates the server provides through the small idempotent `ingest_setup` tool. The queue is `work_list`; a claim is a compare-and-swap, a `concept_patch` with `if_match`, and the lease is the day-granular `review_after`. The source ledger gains an optional `source_shape` (`document`, `collection`, `feed`, `system`) beside the free `source_kind`, and the names `connector` and `recipe`, never credentials.

**Why.** The server stays model-less and never fetches; a campaign that lives in the KB can be resumed, extended and executed by several agents. A claim through the ordinary write tool keeps every write gate and needs no new queue or claim tool (D302 rejected writes on the view). Templates cannot ride on a tool call the way D1072's does, since plans and units are written with `concept_write` / `concept_new`; one tool does the file part. `review_after` is a date judged by day, and an `in-progress` unit is open by its status, so a passed lease never becomes a `review` entry: the queue is defined client-side as `open`, or `in-progress` with `review_after` before today (UTC). A lease shorter than a day is not expressible.

**Alternatives rejected.**
- Changing `workReason` so a passed lease is a `review` entry: it would alter `by_reason` and the entries of every KB's `blocked` / `in-progress` pages.
- Turning `source_kind` into a closed enum: it breaks every existing ledger and its `field_values`; a separate optional `source_shape` keeps it.
- A `claim` tool or write operations on `work_list`: a second path around the write gate.
- Declaring the templates on a map in `ingest_setup`: it makes the map strict (D371, D907) and the planner chooses the Maps.

**Consequences.** `claimed_by` is self-declared and a claim is not authorization: isolation between executors is by token scope. `TestIngestQueueAndLeaseExpiry` pins the empty `reason`; if `workReason` changes, it says so. The feed cursor and polling (#1262) add optional fields to the Source template and the handler's parameter list, the only two places that name them.
