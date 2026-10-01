---
topic: control-plane
---

# D294 — lint and kb_status stay fast on real KBs

**Decision.** `lint.Run` resolves broken-link existence and concept-to-file
mapping from the set of concepts it already enumerated in its walk, instead of
calling `resolveConceptRelPath` (two `os.Stat`) and `ReadConcept` (read + parse)
per link. `kb_status` caches the whole-KB lint findings on the graph-cache
generation and `lastDoctorDate` on the log file's mtime+size; the per-caller
visibility filter (D226) is always applied outside the cache.

**Why.** D290 made `kb_status` run a whole-KB lint on every call. On a
~740-concept work KB with ~5,000 links, `kb_status` went from 0.22 s to 2.2 s —
10× slower and 10× over D290's own 200 ms threshold. CPU profile: 49 % of
`lint.Run` was in `resolveConceptRelPath` (`os.Stat`) and `ReadConcept`
(existence test via full file read), all answerable from the walk lint already
did.

**Alternatives rejected.**

- _Timer-based cache_ — a fixed TTL does not invalidate on writes and shows stale
  data after a quick concept_write + kb_status cycle. The graph generation
  already carries the right semantics: a write bumps it, a read does not.
- _Per-link `os.Stat` with a per-Run file-exists cache_ — still two syscalls per
  new target in a run; the enumeration map is zero additional syscalls.

**Consequences.**

- The `relPathOf` map built during the walk must mirror `resolveConceptRelPath`'s
  "direct form wins when both exist" rule; the oracle test pins this.
- The conformance cache stores unfiltered findings; `uiVisibleFindings` must
  always apply the visibility filter on every call so a restricted caller never
  sees hidden concepts from a warm cache (D226).
- A write between two `kb_status` calls invalidates the cache because it bumps
  the graph generation; an out-of-band file edit does the same (the graph cache
  re-validates on the next `graphView()` call).
- Amends D290's "computed on every call" to "computed once per generation, then
  cached". The cache key is the generation, not a timer.
