---
topic: control-plane
---

# D854 — A pair linked both ways is not a duplicate candidate

**Decision.** `duplicate_candidate` skips a pair when each page links the other in its body (any link outside code; `k.Links()`, D150/D241/D310). A satellite and its parent, and titles made of the same words (stop-words and order dropped), never close on links; for the latter the action asks to retitle one first. In a resource group the item stays while any similar pair is open.

**Why.** The action advised "link them saying how they differ", yet linking never closed the item: the only exit was a page-wide `lint_ignore` that also hid every later pairing of that page (the cost D345 removed for series pages). Mutual links mean someone edited both pages; a satellite and its parent link each other by construction; a link does not tell same-word titles apart in search or the index. It costs one more graph lookup per candidate pair.

**Alternatives rejected.** A one-way link closes it: the duplicate's own "see also" would silence it, often in the commit that created it. Reword the action and document `lint_ignore`: keeps the page-wide dismissal, and a map-level ignore would hide duplicates map-wide (D159). A pair-scoped dismissal syntax: new grammar, staleness and `concept_move` rewrite, the review state D298 declined.

**Consequences.** Pairs already linked both ways stop appearing; existing `lint_ignore: [duplicate_candidate]` keeps working, no migration. Removing either link, or titles converging to the same words, reopens the pair. Each rule is pinned by its own case in `TestReviewDuplicateLinkedPair`. Other kinds with a dismissal wider than the item (`repeated_fact`, `map_naming`, `lint_judgement`) are a follow-up of #834.
