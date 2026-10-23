---
topic: architecture
---

# D1146 — Retirements are rows of the Activity timeline, not a tab of their own

**Decision.** This amends D820, whose route and archive rules stand: the Retired tab of Activity is gone, and a concept that is
archived, deprecated or superseded shows on the Activity timeline as the row of
its change, with a pill naming its status in place of the change pill and, under
the title, what replaced it, why and where it was. The summary counts the
retired rows of the window. `GET /api/ui/v1/kbs/{kb}/retired` stays and is what
the timeline reads; `?panel=retired` opens Activity. A window with no change
shows a quiet notice in place of the summary and, under it, the newest earlier
changes on the same timeline, read in the same round as the window so the page
never shows the notice alone while they load (a failed read leaves the notice
without them).

**Why.** Activity is a timeline bounded by the window the reader picks; Retired
was an inventory with no window under the same header, so the two tabs read as
two different pages answering one question. A retirement is a change that
happened on a day: on the timeline it lands beside the work that caused it, with
the same colours, authors and filters, and the reader learns it was retired from
the row they already scan. The cost is the whole-KB inventory: "every page this
KB ever retired" is now the All window read for its pills, and the per-Map
archive and journal-harvest counts the tab drew are no longer on a page.

**Alternatives rejected.** Keeping the tab: two views of one history with
different rules, the confusion this change removes. A rail entry for the
archive: D820's reason against it still holds, most KBs retire little. Synthetic
timeline rows for retirements with no change in the window: the archive and
supersede tools always change the concept, so the row exists; a status set by
hand is also an edit, and its row says no retirement is recorded.

**Consequences.** A retired concept's row is matched by its current ID, so a
concept moved to an archive Map carries its retirement on the move's row. A
narrowed principal that cannot read the retired route sees plain change rows.
The archive badge on the rail and search's `include_archived` (D820) are
unchanged.
