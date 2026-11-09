---
topic: control-plane
---

> **Amended by D1259**: Health also shows one read-only line saying which worker maintains the KB and when its doctor last ran (upkeep, not a finding).

# D948 — Each Atlas page answers one question: Health what is wrong, Work what waits for a person

**Decision.** Health shows the lint's findings and nothing else: what is wrong
with a page, and when nothing is, the same heatmap all green with the rule
families it was read against. Its findings are grouped by the category of their
rule, one folding row each with the first open, and the git conflicts a sync left
are the only other section. What a person has to answer or decide moves to
Work, in the column of what they do with it: contradictions (no longer repeated in
Health) and gaps through the
`reason` of `work_list`, the searches that found nothing under "to answer", the
proposals of `kb_review` under "to decide". The Health trend is gone. The other
pages follow the same rule: Secrets reads like the Artifacts catalog (a card per
Service and per file, the same detail panel); Settings shows each key with its
whole description and its value, and the default, origin, environment variable
and flag only when the key is opened; the Client tab, before any client of the
viewer has reported, shows `.cartographer.yaml` at its defaults, served as
`client_template` by `GET …/settings` from the client schema; an artifact kind
the KB has none of is an outline tile where its first artifact would go.

**Why.** The pages had grown by accretion. Health mixed defects with open
questions, a trend and failed searches, so a reader could not tell what to fix
from what to decide, and the count at the top measured neither. Settings put
every key's provenance on every row, which is noise until one asks why a value
is what it is. The cost: Work is now the one place that reads several sources
(`work_list`, the status, `kb_review`), so it makes more requests than before.

**Alternatives rejected.**
- Observations in Health under a lower severity: #947 removes severity, and a
  finding Health shows must be one a person can fix.
- A separate review page: one more page to visit for the same person's open work.
- Settings with an All/Changed filter and a "set by" badge on each row: the
  origin answers a question few readers ask, so it waits behind a click.
- Moving the Client tab under the viewer's profile now: left to the bridge work
  (#899); the template keeps the tab useful until a client reports.

**Consequences.** A check that is a judgement belongs to `kb_review`, and so to
Work, never to Health. `client_template` is rendered by the same
`settingschema` that writes the file, so the Atlas and the file never disagree.
A KB without a `skills/` folder has no skills, which is not an error.
