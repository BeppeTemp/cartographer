---
topic: control-plane
---

# D1354 — `search` restores the v0.26 read path: 20 hits, a related block of 5, no section named

**Decision.** `search` without `limit` returns up to 20 hits again, and its `related` block is on by default with 5 entries again (the defaults of D70 and D781). A hit no longer carries `section`: the snippet may still be excerpted from the section that matched best, but nothing in the answer, the tool description or the generated guidance steers the agent to read that section alone. `read: "whole"` on a hit and the map key `read_whole` stay, because they push toward whole reads. The tool description loses the default-limit sentence and the section instruction; the managed block's orientation bullet goes back to "`concept_read` to read;". Compact JSON (D1185) and `frontmatter_raw` only on an unparsable frontmatter stay.

**Why.** On 50 real tasks over a ~900-concept operational KB, run with the same snapshot and a blind judge, v0.27 (limit 8 without `related`, a hit naming its section) cost about 10% less per run than v0.26.1 and answered worse: fully correct 62% fell to 46%, the mean score 1.60 to 1.40, and per task v0.27 lost 11, won 3 and tied 36 (#1354). The ranking is identical, so the difference is what reaches the agent: 8 hits instead of 20, 48 of 160 `concept_read` calls by section against 33 of 155, `graph_context` used half as often, more reads of the repositories. The pages that suffered are the ones whose answer sits whole in one page the agent must find and read. The maintainer decided accuracy comes first and tokens are the price (no budget for confirming runs). The price is a larger default answer, about twice the bytes.

**Alternatives rejected.**
- Keeping the 8 hits and flagging more pages `read: "whole"` by type: the first query still shows too few hits, and the flag helps only after the page was found.
- Confirming runs to isolate #1219 from #1228 first: no budget, and both changes move the agent the same way.
- Restoring only the limit and keeping the section: the section hint is the part of #1228 that changes what the agent reads, and the benchmark pairs it with the drop.
- Dropping `read: "whole"` and `read_whole` too: they steer toward whole reads, the direction the benchmark favours.

**Consequences.** Amends D1185 (the defaults of `search`, and its rule that the answer-size budget is never raised), D781 (the default of `related` is 5 again, as it was) and D1207 (a hit names no section; `read: "whole"` and `read_whole` are unchanged, and the section index only picks the snippet). `TestSearchDefaultAnswerBudget` is re-baselined to the restored default: the number is the measured average of its fixture plus a margin, written with the reason beside it, and it moves only with the default, in a decision. `TestSearchDefaultLimit` pins 20 hits, `TestSearchRelated_*` the block of 5, and `TestGuidanceDoesNotPushSectionReads` forbids guidance that sends an agent from a hit to a section. The Atlas search endpoint keeps its own `limit` and `related: 0`. Once the server runs on a real KB again, the benchmark of #1354 is the check; it is not in CI.
