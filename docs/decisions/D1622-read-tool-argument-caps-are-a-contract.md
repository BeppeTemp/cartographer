---
topic: control-plane
---

# D1622 — read-tool argument caps are a contract

**Decision.** A read tool's arguments that scale the work it does have a fixed cap, and a call over the cap is refused with an error that names the cap and the size it received. `search` and `graph_context` take a query of at most 1024 bytes and 64 terms, counted the way both search backends split terms, whichever count is larger. `concept_list` and `work_list` take at most 16 `where` clauses. `work_list` takes at most 8 `order_by` keys and 8 `fields`. The one exception is `graph_neighbors`: its depth is clamped to 1-12 and the response reports the depth it used, as `graph_path` already does with `max_hops`. Searches the server builds from stored titles (`link_suggest`, reconcile) shorten their own query to stay under the caps.

**Why.** Any read token could make the server do work proportional to an unbounded argument, so one caller could monopolise the server's CPU. A cap bounds that work. Refusing a call instead of truncating it keeps the answer honest: a query or filter that is silently shortened answers a different question, and the agent cannot tell. The cost: a caller with a legitimately long query has to split it.

**Alternatives rejected.**
- Truncate over-long arguments: the result looks valid but answers a question nobody asked.
- Rely on a per-request timeout: the work is already spent when the timeout fires, and a timeout gives the caller no indication of which argument to shrink.
- Refuse an over-deep `graph_neighbors` too: a depth has a natural meaning when clamped, and `graph_path` already clamps.

**Consequences.** The caps are part of each tool's interface: raising or removing one is a contract change with its own decision file, and the "Argument caps" entry of `docs/control-plane.md` is their only list (`internal/mcpserver/argcaps.go` points to it). A new read tool whose cost grows with an argument needs a cap from the start. A query the server builds itself must respect the caps instead of relying on the refusal.
