---
topic: data-plane
---

# D301 — A KB stays cheap to read and to maintain

**Decision.** Cartographer keeps a KB cheap for an agent, not only conformant.
A map may hand its concept list to the server (`index: generated`): the server
rewrites a marked block of its `index.md` after every write, in the same commit.
Read cost becomes visible: `bytes` on `graph_context`/`graph_neighbors` results,
`kb_status.read_cost` (concept and neighbourhood size percentiles over what the
caller sees), and `result_bytes` on audit completions. The two structural causes
of fan-out become `kb_review` kinds, `repeated_fact` and `read_hotspot`, ranked
after `zombie_work`. A link-only links-section item whose target already links
back is `reciprocal_link_item`, an opt-in mechanical fix. Thresholds are
constants pinned by tests, overridable per map (`repeated_fact_min`,
`hotspot_in_degree`, `hotspot_bytes`, `oversize_bytes`).

**Why.** On a large work KB the operator measured that one page update pulled in
many others. Most of the write cascade was a single file: a journal index the
agent re-read and re-sent in 38 % of sessions to add one line, because the KB's
rule was "a new page also goes in its map's index". Reads cost more than writes:
a page plus its neighbours was ~37 k tokens at the median and megabytes around
the hubs. Duplicated facts and hand-written reverse links made each change touch
N pages. The server already holds the data for all of this (graph cache, stat
signatures, backlinks), so it can measure it and do the mechanical part. It does
not judge content (D14).

This refines three decisions. D107: an index can be generated, but only inside a
marked block of a map that opted in. Curated prose outside the block is never
touched. D298: two new kinds, still candidates the doctor decides. D287:
reciprocal items may go, but only one at a time, only when the reverse edge
exists, and only when the operator asks. The section itself stays.

Costs: every write now reads the descriptors of all maps and rewrites generated
indexes whose bytes changed. The tool-description budget (D285) was met by
removing the enumerated kinds and fixable checks from the `kb_review` and
`kb_repair` descriptions: those lists grow every release, and the items and
findings already name them. A narrowed caller's `read_cost` is computed fresh on
every call. Percentiles over a filtered cache entry would still count hidden
concepts.

**Alternatives rejected.**
- *Regenerate only the maps the write touched*: this needs every handler to
  report what it moved, and it never heals an index edited out of band.
  Recomputing every generated map is in-memory work plus one small read per map.
- *Let the server own the whole `index.md`*: operators keep curated prose there,
  and losing it would be the cost of opting in.
- *A hand-maintained index with a smarter `index_patch`*: the agent still reads
  and re-sends the file. The cost is the round-trip, not the edit.
- *Rewrite duplicated facts or split hotspots automatically*: that is a content
  judgement (D14), and the right owner of a fact is the operator's call.
- *Drop reciprocal items in `auto_repair` by default*: a reader of the raw file
  loses the reverse link. That trade is the operator's (D287).
- *Raise the `tools/list` budget to 23 KiB*: the descriptions shrank without
  losing a calling rule, so no new budget was needed.
- *Log read sizes with content hashes or excerpts*: the size alone measures
  amplification, and it is safe to keep.

**Consequences.** A generated block's bytes depend only on the map's direct
concept set and their titles. A test pins that, and also that bytes outside the
block never change. `index_patch` refuses an edit inside the block, and
`concept_move` leaves a generated index to the server. `index_stale` replaces
`index_incomplete` for a generated map. `result_bytes` is the last field of the
canonical audit encoding and is omitted when zero, so logs written before it
verify unchanged. A future audit field must keep that property. Every new review
kind needs a fixture in `reviewFixtures`, and every new fixable check needs a
case in `TestFixableChecksCoverEveryEmittedFix`. `reciprocal_link_item` stays
out of `conformanceChecks`. The `kb-doctor` skill (2.1) proposes
`index: generated` and the reciprocal opt-in once per KB. Updating a KB's
`instructions.md` rules about indexes or reverse links is the skill's job, not
the server's.
