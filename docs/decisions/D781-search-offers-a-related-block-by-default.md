---
topic: control-plane
---

# D781 — `search` offers a `related` block from the link graph, on by default, off with `related: 0`

**Amends D780.**

**Decision.** `search` appends `related` to its response: up to 5 concepts
(`related` argument: 0 turns it off, above 10 means 10) that a personalized
PageRank from the hits reaches, each `{id, title, via, bytes}`. The seeds are
the leading full-match hits weighted `1/(1+rank)`, exactly `graph_context`'s
seeding, and the ranking is the one function both tools call. A partial
(OR-fallback) hit, an archived hit and the archived fallback seed nothing;
every returned hit and every retired concept is left out of the block; the
block is omitted, not empty, when there is nothing to add. The hits, their
order and their scores are unchanged. The graph is projected through the
caller's visibility before anything is computed (D242), so a narrowed token
gets what an admin gets on a KB without the hidden concepts.

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
  `kb.LinkGraphOfLastView` projects the snapshot the ranking just validated
  (7 ms), a test pins that the block adds no validation, and
  `BenchmarkSearchRelated` keeps the number in the repository.
- *Stamping related concepts as surfaced:* D778's statistics mean "what a search
  returned"; the block is a suggestion.

**Consequences.** The agent-profile `tools/list` (D285) had 17 bytes of headroom
and the `related` property costs 29: the `search` description lost 15 bytes of
wording rather than the budget growing. A `search` response grows by up to
~0.7 KB when the hits have links, and by nothing when they do not. `scope`
narrows the hits, not the block: a link can leave the map, and a narrowed token
still sees only what its policy allows. **To revisit:** if the transcripts later
show `graph_context` calls rising on their own, set `searchRelatedDefault` to 0
and keep the argument; if they stay near zero and agents read `related` entries,
the default stays.
