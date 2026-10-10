---
topic: control-plane
---

# D370 — The server keeps one lint sample a day, and Health draws the trend

**Decision.** When the whole-KB lint cache (D294) is recomputed, the server
records `{at, total, by_severity, by_handler}` (handler as in D365) into
`.cartographer/lint-history.jsonl`, next to the auto-repair log: at most one
sample per UTC day (the latest of the day wins), 90 days kept, pruned on every
write, the file rewritten whole through a temporary file. The maintenance
summary exposes it as `lint_history`; `kb_status` exposes `lint_trend` (first
and last sample of the last 7 days, and the delta). Both are whole-KB only, like
the rest of the maintenance summary. Health draws "188 → 40 this week" and a
sparkline beside *Done by Cartographer*, and nothing with fewer than two
samples.

**Why.** D365 says who acts on each finding but not whether the KB is getting
better, and the server only held the latest lint. The totals are derived from
the KB, so they belong in the server's local state, not in the KB's git history
(a commit a day of noise, and a revert would rewrite the past). One sample a day
is enough for a weekly trend and bounds the file to 90 short lines. The samples
are the unfiltered whole-KB totals, so a narrowed principal could not be shown
them without disclosing counts of pages it cannot see.

**Alternatives rejected.**
- Store it in the KB repo: derived state in the history, one commit a day.
- A sample per recompute: a busy KB would write on every edit burst; the file
  would need compaction to say the same thing.
- Append-only JSONL: the "latest of the day wins" rule needs a rewrite anyway;
  at 90 lines the rewrite is cheaper than a compaction pass.
- A history for narrowed principals, computed per caller: it would need a lint
  per principal per day; the trend is for whoever tends the KB.

**Consequences.** `.cartographer/` is excluded from the lint cache key
(`lintInputsStamp`): recording a sample must not invalidate the cache it was
taken from (lint never reads that directory). The history is lost with an
ephemeral volume and restarts from the next lint; the trend simply stays
hidden until two samples exist. A failed write is ignored: the history is never
a reason to fail a lint.
