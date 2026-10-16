---
topic: control-plane
---

# D781 — `search` offers a `related` block from the link graph, on by default, off with `related: 0`

**Amends [D780](D780-graph-context-is-named-where-agents-choose-tools.md).**

**Decision.** `search` appends `related` to its response: up to 5 concepts
(`related` argument: 0 turns it off, above 10 means 10) that a personalized
PageRank from the hits reaches, each `{id, title, via, bytes}`. The ranking is
the one function `graph_context` calls (`rankNeighbours`, restart 0.25, weights
`1/(1+rank)`); the seed set is not the same. `graph_context` seeds from its own
pool, the top 5 hits of the query with no `scope`, partial and archived hits
included. The block seeds from the hits `search` returned, after `scope`, the
limit and the archived filter: the leading full-match hits, at most the first 5
of the response, so the seeds are what the reader sees. A partial (OR-fallback)
hit, an archived hit and the archived fallback seed nothing; every returned hit
and every retired concept is left out of the block; the block is omitted, not
empty, when there is nothing to add. `via` is never a retired page: a retired
page is a step of the walk, so when it would be the `via` the entry names
another live step of equal distance, else the nearest live step on the way,
else leaves `via` out. The hits, their order and their scores are unchanged.

The graph is projected through the caller's visibility before anything is
computed (D242), so a narrowed token gets what an admin gets on a KB without
the hidden concepts. A caller who sees the whole KB passes no filter. A narrowed
one reads the visibility decisions the ranking already made, kept for the
request in its context, so the block asks nothing the ranking did not.

The block reads the snapshot the ranking's centrality prior has just validated
and adds no validation. When that validation failed the snapshot is an older
one that can name a concept deleted since, so the block validates again and is
left out if that fails too. The Atlas search (D286) passes `related: 0`: it
searches as the reader types and has nowhere to show the block.

**Why.** D780 named `graph_context` where agents pick their tools and kept this
block as #781 until the transcripts said whether naming was enough. On 99 real
searches the block raised the share of later-read concepts the response covers
from 73% to 80% and recovered 9 of the 27 searches whose reader went past the
top 10, for about 1 KB per search, roughly what the reads it saves cost. The
measured gap was adoption (0 `graph_context` calls in 124 sessions), and a
structural index earns its gain inside the tool the agent already calls
(arXiv:2606.22417). The decision is taken before the 2–3 week count because it
is made reversible by one constant: the default is `searchRelatedDefault`.

**Alternatives rejected.**
- *`related: true` opt-in:* it moves the adoption problem one level down; an
  agent that never discovers `graph_context` does not discover a flag.
- *Always on, fixed size, no opt-out:* a probe or a token-tight caller has no way
  to say no.
- *Fusing the graph into the ranking:* already measured and rejected in D780
  (first relevant hit at rank 1 from 51% to 24%).
- *Seeding from partial hits too:* a weak match would amplify itself through its
  neighbours; the block would then be strongest where the text evidence is weakest.
- *Calling `visibleGraph`:* it validates every concept file a second time, which
  is 95% of a graph read; on 3000 concepts it cost 150 ms on a 680 ms search.
  `kb.LinkGraphOfLastView` projects the snapshot the ranking just validated and
  a test pins that the block adds no validation.
- *A visibility check per concept for a non-admin caller:* each one reads a
  `_map.md` and stats a facet. On 3000 concepts it added about 1.1 s to a
  0.27 s search for a token that sees the whole KB. A whole-KB caller passes
  no filter, and a narrowed one reuses the ranking's decisions.
- *Showing the block in the Atlas search:* a surface of its own to design, out of
  scope here; the Atlas passes `related: 0`.
- *Stamping related concepts as surfaced:* D778's statistics mean "what a search
  returned"; the block is a suggestion.

**Cost.** `BenchmarkSearchRelated` runs, on 3000 linked concepts, a search
with and without the block back to back and reports the median of their
differences (`overhead-ms/op`), because on a loaded machine only a difference
taken inside one run means anything. One run on such a machine: an admin +3.6 ms
on a 249 ms search, a whole-KB token +9 ms on 252 ms, a narrowed token within
the noise of its 1.4–1.8 s search (−230 to +99 ms over three runs), the memo
having made the block's visibility free. These are per-run figures, for that
fixture and that query, not constants: run the benchmark to get one for a
machine.

**Consequences.** The agent-profile `tools/list` (D285) had 17 bytes of headroom
and the `related` property costs 29: the `search` description lost 15 bytes of
wording rather than the budget growing. A `search` response grows by up to
~0.7 KB when the hits have links, and by nothing when they do not. `scope`
narrows the hits, not the block: a link can leave the map, and a narrowed token
still sees only what its policy allows. **To revisit:** if the transcripts later
show `graph_context` calls rising on their own, set `searchRelatedDefault` to 0
and keep the argument; if they stay near zero and agents read `related` entries,
the default stays.
