---
topic: project-governance
---

# D1425 — GitHub Discussions are off, and an idea from outside is an issue

**Decision.** The repository has no Discussions. An idea reaches the project as an issue
from the `idea` template, with the label `idea`: a proposal, not accepted work. At triage a
maintainer puts it in a milestone, promoting it in place to a `plan` when it is not trivial,
or closes it with the reason. A question starts from `docs/faq.md`, and one the docs do not
answer is a documentation bug. Where a maintainer keeps the ideas not accepted yet is not
part of the repository.

**Why.** Discussions were one more place to read and to triage, and in practice almost
everything there was written by the maintainer and then moved to an issue. Every lasting
answer already belonged in `docs/` (D100). The cost is that a contributor can no longer
browse the open design in public, and that a link to a Discussion from an old issue or a
released `cartographer-feedback` skill no longer resolves.

**Alternatives rejected.**
- No channel for ideas, only `bug` and `plan`: a contributor would have to write a whole plan
  to suggest something, and the feedback skill would lose its idea URL.
- Keeping Discussions open for contributors only: the same surface to triage, for very few
  posts.
- Ideas as `bug` issues: it mixes proposals with defects, and `bug` means accepted once triaged.

**Consequences.** This reverses the part of D955 that dropped the `idea` template and D100's
Discussions. D955's worry stands, a contributor's agent acting on an idea with no plan, and the
gate still answers it: `plan-gate` reads `plan` and `approved`, and an `idea` label satisfies
neither. An open `idea` issue has no milestone until triage, like any issue not triaged yet.
