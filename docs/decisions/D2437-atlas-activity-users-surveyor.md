---
topic: control-plane
---

# D2437 — Activity splits the people's changes from the Surveyor's runs, and a row opens its diff

**Decision.** The Atlas Activity page has two tabs with one shape. **Users** lists the changes people made: a change whose authors are all Surveyor authors is left out, and a shared one shows its human authors only. **Surveyor** lists the runs, each a row with what the agent did (task, outcome, duration, pages, tokens on the window's scale, replica) and, hung under it, the pages it wrote, drawn as the Users rows; a page is listed under one run only, the newest whose span and replica claim it. Both tabs open with five figure tiles, a stacked wave of the window (When) and the filter chips (Where and Who, or What and Where). Clicking a row opens, under it, the concept's diff over the window or the run (D2436), file by file and folded, instead of jumping to the graph; each file opens the whole file or the page, and the inspector's × goes back to the row it was opened from.

**Why.** One timeline mixing the two answered neither question: a reader looking for what a colleague changed scrolled past dozens of Surveyor edits, and a reader checking the Surveyor saw runs and pages as unrelated rows. The diff is what a reader of a change wants first; the page in the graph is one click further. One row component for both tabs keeps the vocabulary of the other pages (Work, Health): tiles, hue wheel, timeline.

**Alternatives rejected.**
- A Surveyor pill on mixed rows in one timeline: the volume of the Surveyor's writes still buried the people's.
- Runs and pages as sibling rows on the rail: a run and a modification read as the same kind of thing.
- Opening the page on click, with a diff tab in the inspector: two steps for the common question, and the reader loses their place in the timeline.

**Consequences.** Changes are aggregated per concept by the server, so a concept both a person and the Surveyor changed in the window shows on Users with the time and reason of its last change, whoever made it. A run's pages are matched by replica and time, so runs that overlap in time can claim each other's pages: the newest wins.
