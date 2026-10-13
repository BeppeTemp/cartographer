---
topic: sync-provisioning
---

# D780 — `graph_context` is named where agents choose their tools, not fused into `search`

**Decision.** The first generated "Operational instructions" bullet, the line
that tells an agent which tools to consult a KB with, also names
`graph_context`, for answers that span several linked pages. Its tool
description says when to use it, one call instead of chaining `search`,
`concept_read` and `graph_neighbors`, in place of listing its response fields.
The `search` ranking is unchanged.

**Why.** `graph_context` (D242) was called 0 times in 124 agent sessions of one
installation since it shipped, while `search` was called 198 times and 24% of
`concept_read` calls opened a page linked from one read earlier in the session,
which is the walk `graph_context` replaces. Nothing an agent reads at session
start named it: the generated bullet listed `search`, `atlas_overview` and
`concept_read`, and the only sentence saying when to prefer it lived in
`docs/control-plane.md`. A structural index earns its gain inside the tool the
agent already calls (Bhola et al., *Code Isn't Memory: A Structural Codebase
Index Inside a Coding Agent*, arXiv:2606.22417, 2026); naming the tool where
agents pick their tools is the cheapest test of whether adoption, not ranking,
is the gap. It costs 136 bytes per KB in every steering file.

**Alternatives rejected.**
- Fusing the graph into `search`'s ranking (reciprocal rank fusion of the
  `search` and `graph_context` lists): on 99 real searches replayed against
  the same server, with the concepts the agent then read as the relevant set,
  the first relevant hit at rank 1 fell from 51% to 24% and in the top 5 from
  82% to 61%, because `graph_context` leaves its seeds out of its results.
- A `related` block appended to every `search` response: the share of
  later-read concepts the response covers rose from 73% to 80% (9 of the 27
  searches whose reader went past the top 10 were recovered), for about 1 KB
  more per search, roughly what the reads it saves cost. Kept as
  [#781](https://github.com/BeppeTemp/cartographer/issues/781), to decide on
  measured usage after this change.
- A pointer in `search`'s description: the agent-profile `tools/list` budget
  (D285) had under 80 bytes left, and the steering line reaches the agent before
  any tool list does.

**Consequences.** A KB whose `instructions.md` opts out of the generated bullets
(`preamble: none`) does not get the line; its curated text decides which tools
it names. Whether this was enough is read from the client transcripts, not
from the server: a later change to how structure reaches agents starts from
that count.
