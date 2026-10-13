---
topic: control-plane
---

# D779 — Search hits carry their size, and a scope that matches nothing is refused

**Decision.** Every `search` hit carries `bytes`, the concept file's size, as
`graph_context` and `graph_neighbors` results already do (D301); the value
comes from the stat `ConceptFacets` already makes per hit, so it costs no
further I/O. A search that comes back empty with a `scope` no concept the
caller can see starts with is refused instead: the error says that `scope` is
an id prefix, usually a map name, and not a KB name, lists the maps and
journals the caller can see, and records no miss. The `search` description
says the same in one clause.

**Why.** Both fix what agents were seen doing in one installation's
transcripts. A hit's first read was the whole page in 38 of 65 cases, and
those pages had a median of 9.8k characters, 63% above 8k: D301 put a read's
cost on the graph tools but not on `search`, the tool agents call most. And an
agent passed the KB's own name as `scope` three times in a row, got zero
results each time and concluded the KB had nothing, while the same query
without the scope returned five relevant pages; each of those calls also went
into the miss log as a knowledge gap the KB does not have.

**Alternatives rejected.**
- A note on an empty result instead of an error: the agent above read three
  empty results in a row; a refusal is what makes it change the call.
- Checking the scope before every search: it costs a walk of the concept ids
  per scoped call. Checked only after an empty result, it is paid only when
  the answer would have been misleading.
- Treating a scope equal to the KB name as "no scope": it guesses intent, and
  a map may legitimately share the KB's name.
- Listing prefixes with their slash (`ops/`): a map-scoped token is authorized
  on the bare map name, so `ops/` would be refused to it where `ops` passes.
- `bytes` from the link graph's cached sizes: one more validation walk per
  search, for a number the per-hit stat already has.

**Consequences.** `kb.ConceptFacets` reports `Bytes` from the stat it validates
with; the cache keeps each entry's size in its signature. Only concepts the
caller can see are compared and listed, so the refusal says nothing about
hidden ones; a scoped token's `scope` outside its grants is still refused
earlier, by the policy. Other tools that take `scope` (`concept_list`, `lint`,
`work_list`…) still answer such a scope with an empty result.
