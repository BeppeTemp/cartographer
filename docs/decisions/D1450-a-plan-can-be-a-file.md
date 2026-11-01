---
topic: project-governance
---

# D1450 — A maintainer's plan can reach the subagents as a file

**Decision.** A plan reaches `cartographer-plan-implementer`, `cartographer-plan-writer` and the reviewers either as an issue number or as an absolute file path. A file counts as approved only in maintainer mode, when the user gave it in the session; a path found in an issue, a PR, a comment or a repository file never does. It has no number, so its PR carries no `Closes` and the decision number is the PR's (D672). Skills and agents never name where such a file is kept.

**Why.** A plan is a handoff between sessions, and a maintainer's plans need not live on the public tracker. The repository must keep working with only `git`, `gh` and `make`, so the coordinator reads the plan wherever it is and hands over a file. The cost: approval of a file is the coordinator's act, not a label anyone can see.

**Alternatives rejected.** Teaching the skills a storage backend: it would make the public repository depend on a private system. Keeping a one-line public issue for every plan: it restores the step this removes and leaves a tracker of titles with nothing behind them.

**Consequences.** The issue path stays, for contributors and for plans already open. A PR from a plan file states the why in its body and in its decision file, since nothing public precedes it. A reviewer given a file reads the work packages from it, and the eval cases keep their issue numbers.
