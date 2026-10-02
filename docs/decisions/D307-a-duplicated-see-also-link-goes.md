---
topic: data-plane
---

# D307 — A duplicated "See also" link goes, and lint stops raising non-questions

**Decision.** Every `duplicate_link` carries a mechanical fix. The link leaves its
"See also" item together with its words. An item that holds other links keeps
them (`rewrite_link_item`), one left empty is dropped, and an empty section loses
its heading. Three other checks stop raising questions that have no answer:
- `map_misfit` neither flags nor counts a retired concept;
- `glossary_gap` ignores universal technical acronyms and all-caps emphasis of
  ordinary words;
- the Atlas no longer colours nodes by lint severity, so its legend has no
  Health mode.

**Why.** The operator's rule, once asked: a link already in the text does not
need repeating in "See also". The 137 duplicates on a real KB were all of that
kind. Before this, only link-only items were fixable, because "the words may be
the reason". The text that links the target already says why, so the item's
words repeat it, and they drift apart. In the same KB `map_misfit` flagged
archived pages, which live in the archive because they are retired, and
`glossary_gap` asked for definitions of API, HTTP, GB and the Italian "NON" (66
items). An empty Observatory is the goal (D306), so a check must not raise
something no one can answer except by dismissing it. The cost of dropping item
words is that a reason written only in the item is lost. The fix runs only
where the text links the same target, which is where the reason belongs.

**Alternatives rejected.**
- *Accept `duplicate_link` map-wide*: it keeps the repetition D287 calls a
  drift risk, and the operator did not want the sections.
- *Move the item's words into the text*: no mechanical rule knows where in the
  text they belong.
- *A per-KB list of common acronyms*: every KB would write the same list. The
  built-in one covers universal terms, and `glossary.yaml` remains for a KB's
  own terms.

**Consequences.** An item that mixes a duplicate and another link inside one
group ("[[a]] and [[b]]") is left alone, because no cut keeps one without the
other. Two-letter all-caps words (`HA`) are always candidate terms, whatever
their lowercase form means.
