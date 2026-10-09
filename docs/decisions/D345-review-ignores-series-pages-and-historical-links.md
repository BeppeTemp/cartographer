---
topic: control-plane
---

# D345 — kb_review ignores series pages and historical links

**Decision.** `duplicate_candidate` (title check) and the creation-time `similar` advice skip two pages that form a series: titles equal but for period tokens (a token with a digit, or `q1`-`q4`/`h1`-`h4`) and id basenames differing only in those positions (`lint.SeriesSiblings`). `zombie_work` counts only links under an origin/dependency heading (built-in English and Italian list), not links in prose.

**Why.** Both were false positives that operators silenced with `lint_ignore`, which also hides future true positives on the same page. Quarterly pages share most title words by construction; a precedent cited in prose is history, not unfinished work, and "close it as obsolete" is wrong advice for it.

**Alternatives rejected.**
- A map-contract key for the origin headings: the `map_update` catalogue is about one byte from the D285 budget.
- Keying the series on titles alone: two unrelated pages with numbers in the title would vanish from review; requiring the ids to follow the same pattern guards that.
- Month names as period tokens: language-dependent, would need a vocabulary.
- Frontmatter or declared link fields as dependency evidence: the link graph is body-only (D241) and no field carries concept links; a separate feature.

**Consequences.** One shared helper serves both call sites; change it with both tests. Origin links resolve through `ExtractLinks` with the concept's real file path (expanded concepts). A KB in another language uses `lint_ignore: [zombie_work]`. `link_to_retired` and `stale_open` still read body-wide links.
