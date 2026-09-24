---
topic: control-plane
---

# D247 — Search misses reveal knowledge gaps

**Decision.** A `search` that finds nothing is recorded, but only for a
principal who can see the whole KB. Each miss goes to
`.cartographer/search-misses.jsonl` with its time and its normalised query
(trimmed, lowercased, diacritics folded as in D246, whitespace collapsed).
`kb_status` reports `search_misses`: the 10 most frequent queries of the last
30 days. No new tool and no configuration knob.

**Why.** A query that returns nothing is the most direct evidence that the KB
lacks something an agent needed, and it used to be thrown away. Graphify keeps a
query log for the same reason. The cost is one small local file, bounded at
2,500 lines and rewritten to the last 2,000.

**Alternatives rejected.**
- *Recording every principal's misses*: for a narrowed token, zero hits may only
  mean "hidden from you", so the entry would be mislabelled as a gap. It would
  also show that user's queries to every whole-KB reader of `kb_status`.
- *A dedicated tool or a lint finding*: `kb_status` is already whole-KB only
  (by resource class) and is already where an agent looks for the state of a
  KB.
- *Committing the log to the KB*: it is local telemetry, not knowledge. It would
  churn history and replicate one server's traffic to every clone.
- *Rotation or an opt-out setting*: the file is bounded and disposable, and only
  whole-KB principals can read it back. A knob would add surface without adding
  safety.

**Consequences.** `search`'s response is unchanged. A write failure is logged
and never fails a search. Rejected queries (empty, removed `mode`) are not
misses. The whole-KB rule is commented at the recording site in `handleSearch`,
and any new place that records misses must keep it.
