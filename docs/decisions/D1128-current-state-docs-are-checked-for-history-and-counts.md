---
topic: project-governance
---

# D1128 — Current-state docs are checked for history phrases and hand-kept counts

**Decision.** `internal/repodocs/doc_rot_test.go` fails when a current-state page
(`docs/` except decisions, the root contract files, area `AGENTS.md`, bundled and
project skills, project agents) carries `used to`, `previously`,
`removed/dropped/retired by|in D<n>`, `as of D<n>`, `became`/`gained`, or a number
of three or more before `providers clients tools checks kinds subagents agents
runners skills modes tiers workflows jobs errors`. Code and inline code are not read.
A sentence about a retired key, tool or flag a reader can still meet goes on a
file-and-fragment allow-list, each entry with a reason, pruned both ways: an
uncovered hit fails and so does an entry that covers nothing.

**Why.** `docs/index.md` says current-state pages carry no history and no counts,
and nothing enforced it; each fix returned at the next edit. A deterministic net
removes the cheap, mechanical half of the contract reviewer's reading.

**Alternatives rejected.**
- Also policing `now`, `no longer`, `since D<n>`, `before D<n>` and `older version`:
  about two thirds of their hits state something a reader can still hit, so the
  allow-list would grow to dozens of entries and lose its meaning.
- Allowing by line number: drifts at every edit; a fragment survives rewrapping.
- Loosening a phrase to spare a retired-key sentence: the allow-list is the only
  exception, so the rule stays narrow.

**Consequences.** The allow-list must stay small and never hold a sentence that
narrates history. Semantic drift stays with the reviewer. A new phrase is added
only with its measured hits on the corpus.
