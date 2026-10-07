---
topic: control-plane
---

# D338 — Health merges the Observatory and Maintenance panels

**Decision.** The Atlas has one **Health** panel where it had an Observatory
(lint findings and knowledge gaps, D307, D319) and a Maintenance panel (background
repairs, doctor sessions, open questions, D323). Its title gives the worst state
in words; a band shows the counts, the upkeep schedule and the knowledge counts;
below come the questions for the reader, the findings, the knowledge gaps and,
beside them, the repair log. `panel=observatory` and `panel=maintenance` links
open Health. No API route changes: the panel reads `lint`, `status` and
`maintenance/{summary,questions}` as the two panels did.

**Why.** Both panels answered "how is this KB doing", each halfway: a reader saw
"nothing to report" on one page while a question waited on the other, and had to
visit both to know whether anything needed them. One page puts what to act on
first and what keeps the KB in shape beside it, and the rail loses an entry. A
whole-KB part the principal cannot read (a `404` on `status` or
`maintenance/summary`) is left out instead of reported as an error: for a narrowed
token that is the expected answer, not a failure, and the findings and its own
questions still show.

**Alternatives rejected.**
- Keeping two panels and cross-linking them: the reader still has to know that
  half the answer lives elsewhere.
- Tabs inside one panel: the same split behind a click, and the title could
  not say the whole state.
- Renaming the routes server-side (`/health`): the UI API reads stay the
  tools' own projections; a UI regrouping is no reason to move them.
