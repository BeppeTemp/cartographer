---
topic: architecture
---

# D1435 — A metrics lister reports verdicts, never values

**Decision.** A system recipe over a metrics source prints, for each signal, one word of a fixed ordered scale computed
from a query run at the source. A unit rewrites the page and, when a line got worse, adds a dated line to a gap; a
`prompt` task reads those gaps on a long cadence, closes the ones back at the best word and raises what no single line
shows. This is a convention with a reference lister (`examples/system-recipes/prometheus-verdicts/`): no code path in
the worker enforces it.

**Why.** The revision of a group is a hash of its content (D1372), so a line holding a value changes at every poll and
costs a unit for nothing a reader wants, and these runs are batch work meant for small open-weight models: the less a
model must judge the better. A word from a short ordered list makes the unit mechanical and an unchanged system free.
It costs the numbers: the KB can say a disk is `bad`, never how full it is, and the bands are the operator's to tune.

**Alternatives rejected.**
- A listing with values and a filter in the worker: the worker would have to know what each metric means.
- Thresholds read by the poll's model: tokens at every poll, and an answer that does not reproduce.
- A lister that compares with its previous run: a system listing has no cursor and a worker no volume, and the source
  already holds the history.
- Free words per signal: they read better, but a small model cannot order them across signals.
- Alerting through the KB: hours late and with no routing. Paging stays in the alerting system.
- Series in the KB: a commit per sample, and a worse store than the one that has them.
- `conflict_resolve` to close a gap: an admin tool outside a prompt task's allowlist. `resolution_status` is frontmatter
  and `concept_patch` sets it with `if_match`.

**Consequences.** The history of a line lives in its gap page, written by a model, so it is as reliable as the unit that
wrote it: a gap is a question for an operator, not a record to compute on. A unit's `extract` and `verify` are limited
to one line of 500 characters and its brief is reused across changes, so a recipe's standing rules belong in the
`body`. The values of `exec.env` are redacted from what a lister prints, so an `identity` read from one comes out masked.
