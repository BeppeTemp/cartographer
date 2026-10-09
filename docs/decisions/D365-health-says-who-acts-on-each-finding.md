---
topic: control-plane
---

# D365 — Health says who acts on each finding

**Decision.** Every finding of `GET /api/ui/v1/kbs/{kb}/lint` carries `handler`:
`auto` when it has a `fix` of a check the background repair runs unattended on
this KB (listed in `auto_repair`, `AutoRepairSafe`, heartbeat on), else
`doctor`. The maintenance summary adds `doctor_mode`. The Health panel is
organised around it: a verdict that answers "does anything need me?", three
lanes (*Automatic*, *Doctor*, *You*), findings grouped by check with the same
message on many pages folded into one line, a *Checks* tab beside *Findings* that lists every
check by category with its count, a zero too (`GET .../checks`, `Category` in the
registry), and a timeline of the background runs of the last 30 days
(`runs` in the summary), each split by check. Severity orders the rows; it does not sort them into piles: an
info finding is an improvement in the doctor's queue. The severity floor and the
accept badges are gone from the panel.

**Why.** With repair on write (D349), fixpoint repairs (D355) and an
unattended doctor (D358) most findings are not the reader's to act on, yet the
panel listed them one per page under "N things need attention": on a real KB
171 of 188 warnings were one cause (types no template declares), and the
upkeep that had fixed 310 pages that day was a truncated line. Who acts on a
finding is a server fact (the registry, the KB's config, whether this finding
carries a fix), so the server says it and the UI does not re-derive the
registry.

**Alternatives rejected.**
- Derive the handler in the UI from `auto_repair.checks`: it misses the
  per-finding `fix` (a runnable check can still need judgement) and the
  `AutoRepairSafe` filter, and duplicates the registry.
- A third handler `person`: nothing in the registry is a person's by
  construction; the doctor decides and records a question when it cannot
  (D358), and those questions are already their own list.
- Infos as a separate *Suggestions* pile for the reader: nobody reads it as
  theirs to act on, and the doctor already owns them (its Advice step fixes
  them or accepts them with `lint_ignore`). Making the mechanical ones
  deterministic is a server change of its own, check by check.
- A trend chart of findings over time: the server keeps no lint history; it
  needs a stored series first and is a separate change.

A check that does not run on a KB (a template check where no map sets
`require_template`, `unknown_type` with an empty palette) still reads 0 on
*Checks*; making every check run by default is a separate change, after which
a zero is always a pass.

**Consequences.** The panel reads "Attention" only when something needs a
person: an error, an open question, or problems with a doctor that never ran or
is overdue; otherwise it says Cartographer is on it. Acceptability stays in the
API and `kb_status` for agents. Checks get a plain-words label in the UI
(`web/src/lib/health.ts`); one without a label falls back to its name.
