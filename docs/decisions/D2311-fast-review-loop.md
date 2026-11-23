---
topic: project-governance
amends: D2040, D857
---

# D2311 — The coordinator runs one hosted check beside the review, and a second review round needs a high or medium finding

**Decision.** A chain of changes of one plan (each change on its own branch and worktree, stacked on its predecessor's and created by the coordinator only, since `make ready` takes the slug from the branch) may be written by one implementer, is reviewed once over its range and checked once on its tip. The implementers (plan, fix, live) run targeted tests and never `make check-hosted`: the coordinator starts one on the tip, in the background, when review round 1 starts, and the rule is one wording everywhere: a fix that changes any non-test, non-doc file gets one more hosted check; the change is queued only after a green hosted check of the last such commit (a doc is a Markdown file under `docs/`; a test is a test file). All verified findings that are cheap are fixed in one round, whatever their severity; on a maintainer's change a second round by a reviewer runs only after a verified `high` or `medium`, else the coordinator reads the fix range itself, unless the triage of that range shows any supply-chain or invariant-area file or a tier above round 1's (then a reviewer round runs, the security reviewer too on full tier); the change's authorship comes from round 1's triage, and every other PR, contributor or bot, always gets the round; round 3 only if round 2 still has one; a `low` left unfixed is a follow-up line or a `bug` issue. A plan merges a change under about 150 production lines into a neighbouring one.

**Why.** Measured on `main`, a same-day full-tier change took a median 66 minutes from the first hosted check to landing, with 2 to 5 hosted checks of about 8 minutes each, run serially after every fix round, and 2 or 3 review rounds, never 1. A docs-only change took 49 minutes, so size was not the cost. The recorded eval scores show rounds 2 and 3 are not what protects `main`: the required checks, the train gate on every leg, the pre-check, the contract reviewer on every change, the security reviewer on the core supply-chain set and the maintainer's read of its hunks are. The cost is that a `low` finding may reach `main` unfixed, and that a fix made while the first hosted check runs can be checked one more time.

**Alternatives rejected.**
- Downgrade the security reviewer for non-core agent-steering files: it amends D857's explicit rule for little time.
- Keep a hosted check per implementer: each is minutes of wall time on the critical path for a tree the coordinator checks again.
- Drop the hosted check before queueing and rely on the train gate: a red train costs more than a check beside the review.

**Consequences.**
- No agent definition under `.claude/agents/` tells an implementer to run `make check-hosted`; `internal/repodocs` pins it.
- D2040's rule that a chain is reviewed and checked once on its tip is now the written procedure in the implement-plan skill and `CONTRIBUTING.md` §Plans.
- D857's three-round maximum stays; round 2 is conditional.
