---
topic: control-plane
---

# D1644 — The entries of a journal map are never `duplicate_candidate` by title

**Decision.** `kb_review` does not compare the titles of the entries of a map whose contract says `kind: journal`. The same-resource rule is unchanged.

**Why.** A journal's entries are named alike by construction ("Daily standup <date>"), so they share most title words. Pairing them costs N^2/2 Jaccard evaluations that grow with the journal, and `SeriesSiblings` then discards almost every pair. The cost buys nothing a reader acts on: merging two journal entries is not the remedy. It costs the rare real duplicate inside a journal, which is no longer reported.

**Alternatives rejected.** Skipping title tokens present in more than X% of the map: a threshold to tune, and it still pairs the rest of a journal. Keeping the pairing and making it faster: the output stays noise.

**Consequences.** `internal/lint/review.go` `duplicateItems` reads the map's `Kind` before indexing a title. A test pins zero comparisons on a journal and a still-reported pair in a non-journal map. A KB that wants duplicates found in a journal changes the map's kind.
