---
topic: control-plane
---

# D226 — Typed read projections and a read-only UI API

**Decision.** The data a browser needs to explore a KB comes from two typed Go
projections — `kb.GraphSnapshot` for the link graph and
`mcpserver.queryConcepts` for the concept inventory — served over
`/api/ui/v1`, a purpose-built read-only JSON surface inside the existing HTTP
auth chain. Neither projection holds authorization logic: the caller passes its
own visibility predicate, so the MCP handlers and the HTTP adapter run the same
walk under their own principal. No new tool is added to the agent surface.

**Why.** The facts were already derivable from the read tools, but only as
prose and Markdown shaped for a bounded agent call. A browser scraping
`concept_list`'s text, or calling `graph_neighbors` once per node, would have
been slow and would have re-implemented the semantics on the far side of the
wire — where a permission rule cannot be enforced. Extracting the projections
costs one more layer between the walk and the tool response, and it means two
consumers now depend on shapes that used to be private to one handler.

**Alternatives rejected.** A `graph_snapshot` MCP tool: it would spend agent
context on a payload no agent asked for, and the browser is not an MCP client.
Letting the UI read KB files directly: it would need a second copy of the
domain model and could not be permission-filtered. Reusing `kb_status`'
aggregate counts for the UI overview: `kb_status` walks the whole KB, so a
narrowed token could have inferred how many concepts it is not allowed to see —
the overview derives every count from the filtered walk instead, and takes from
`kb_status`' territory only the replication facts, which it serves only to a
caller that can already see the whole KB.

**Consequences.** Three invariants now have to hold for every response on this
surface, and each one has a test. No returned edge may have an endpoint absent
from the node list. A concept hidden from the caller is absent as a node, as an
edge endpoint and from every count — and, because it exists, it is not reported
as a broken target either, which would disclose its id. A hidden KB or concept
answers `404`, never `403`: distinguishing them would make the API an existence
oracle. Sorting happens before truncation everywhere, so identical KB state
yields an identical response and a graph does not reshuffle between two
identical requests. `concept_list`'s response bytes, accounting and filter
semantics are unchanged, and the existing tests are the proof.
