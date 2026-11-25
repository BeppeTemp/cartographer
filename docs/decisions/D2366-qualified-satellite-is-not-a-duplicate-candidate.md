---
topic: control-plane
---

# D2366 — A satellite whose added title words are its id segment is not a duplicate candidate

**Decision.** `duplicate_candidate` skips a satellite (a two-level `concept_expand` child) whose title holds every word of its parent's title and whose added words are all tokens of its own last id segment (`lint.qualifiedSatellite`): the `QualifierSiblings` test applied to a parent/child pair. The `suggested_action` of a satellite pair that remains no longer offers linking, and says to merge the child or, for a variant, make its id segment carry the added words.

**Why.** A KB with a generic page and per-language variants (`vulnerability-1`, `vulnerability-1/lang-a-vulnerability-1`) put hundreds of legitimate pairs on the Work panel. Links cannot close a satellite pair (D854), and D907 forbids dismissing it, so rename or merge were the only exits. The id segment already says what the title adds, as D345 treats sibling variants.

**Alternatives rejected.** An explicit `parent` frontmatter key: heavier, kept as a later option if the heuristic is not enough. Letting links close satellite pairs: reopens D854, where a satellite links its parent by construction. Skipping any satellite: hides near-identical titles ("Secrets and Credential Management" / "Secrets Management").

**Consequences.** Only the near-identical-title path changes; the same-`resource` group path is untouched. Anything unclear (same words, a missing parent word, an added word outside the segment, a deeper child) stays a candidate.
