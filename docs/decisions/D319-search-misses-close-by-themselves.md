---
topic: control-plane
---

# D319 — Search misses close by themselves

**Decision.** `kb_status` re-runs each reported search miss against the
in-memory index, reconciled first, and marks `resolved: true` the queries
that now find something. Resolved queries sort after every open one and never
push an open one out of the top 10. `search` takes an optional `record_miss`
(default `true`), and `false` keeps a verification probe out of the log. The
Observatory shows a resolved miss as a ticked, muted chip.

**Why.** A miss (D247) used to stay for 30 days after the gap was filled, so
the report kept asking for knowledge the KB already had. Maintenance skills
that check whether a concept exists also recorded probes as gaps. Checking at
report time costs at most a few in-memory searches, and only `kb_status` pays
for it. The log stays disposable telemetry, with no new state to keep in step.
It uses the in-memory index only: FTS5 may be missing or failing, and the live
index is always there. As a result, a query only FTS5 can match (a substring)
stays open until it ages out.

**Alternatives rejected.**
- Dropping resolved misses from the report: the Atlas could no longer show that
  a gap was filled, so a reader could not tell a closed gap from one that was
  never searched.
- A persisted dismiss list or a dismiss tool: more surface and more state to
  keep in step with the log, for something the re-check already handles.
- Rewriting the log to remove resolved entries: a write on a read path. It
  would also lose the count if the concept is later deleted.
- Not recording a miss for a term other KBs cover in the routed topology (D288):
  a KB cannot know what its siblings cover without a cross-KB query on every
  miss. `record_miss: false` lets the caller who knows say so.

**Consequences.** The checker is installed in `RegisterKBTools` after
`newSearchReconciler`, which it closes over. It runs with no auth filter
because it checks the server's own telemetry, and the visibility gate on
`search_misses` is unchanged. The re-check uses the raw normalised query, not
its glossary variants, because that is what the recording site stores. The
`uiSearch` path still never records a miss (D286).
