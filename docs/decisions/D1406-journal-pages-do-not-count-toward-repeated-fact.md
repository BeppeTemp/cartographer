---
topic: data-plane
---

# D1406 — Journal pages do not count toward repeated_fact

**Decision.** A concept in a map whose contract says `kind: journal` is not counted by `repeated_fact`, in `kb_review` and in the write-time advice alike: the current page of a journal gets no advice, and journal owners do not count toward the 3-concept threshold. A line is reported only when it recurs in enough concepts outside journals.

**Why.** A journal page is a per-run record; runs repeat the same sentence by nature, and the item's action (keep the fact in one concept, link from the others) cannot be taken on them. The map kind is already the declaration, so no new setting and no per-line marker (D907: nothing hides a finding).

**Alternatives rejected.** A `repeated_fact` dismissal or per-map threshold: D907 removed such settings. A per-line marker: every run page would carry it. Counting journal pages but not reporting them: a line shared with a journal would still push two non-journal pages over the threshold.

**Consequences.** A map of granular runs that wants this declares `kind: journal`; plain maps keep today's behaviour. The advice reads one map contract per candidate concept it re-reads.
