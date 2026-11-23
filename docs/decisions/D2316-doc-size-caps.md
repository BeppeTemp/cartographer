---
topic: project-governance
---

# D2316 — A docs page is capped at 1,000 lines and a CONTRIBUTING.md block at 1,500 characters, with ceilings for what is already longer

**Decision.** `internal/repodocs` fails when a `docs/*.md` page (generated blocks and
`docs/decisions.md` aside) passes 1,000 lines, or when a paragraph or list item of
`CONTRIBUTING.md` (nested items counted apart, fences and tables skipped) passes 1,500
characters. What was already longer is listed in `GrandfatheredPages` and
`GrandfatheredBlocks` with a ceiling: its size at the time plus 10%, rounded up to 50.

**Why.** Pages grew by accretion (one grew 81% in three weeks) under advisory signals only,
and a page or clause that long is neither read whole by a reviewer nor retrieved well. A
cap with no margin would make the next line added to a long page force its split inside an
unrelated change; a ceiling gives a bounded margin and makes every raise a visible edit of
the map, the stance of `MaxRootChars`. A listed entry that is back under the cap fails too,
so the lists only shrink.

The lists only shrink: `TestGrandfatherListsOnlyShrink` holds the exact entries and fails
when a ceiling sits more than one margin above its page or block. A block is keyed by its
nearest heading and the first 40 characters of its text, so a paragraph added above it changes nothing. Generated blocks of every
kind are left out of the count.

**Not capped.** The length of one line: a docs page keeps a paragraph on a line and many
pass 2,000 characters, so a line cap would grandfather over a hundred lines; the line
count is the measure and a long line is left to review.

**Alternatives rejected.** Ceilings at today's exact size: the first line added anywhere
forces a split. A `WARN` in the triage: the growth measured happened under warnings.

**Consequences.** One `process` issue per listed page or block asks for its split. A new
page or clause is capped at once. The split of a listed page removes its entry in the same
change.
