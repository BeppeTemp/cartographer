---
topic: project-governance
---

# D957 — The repository's skills and agents take the `cartographer-` prefix and pair by name

**Decision.** Every skill and agent of this repository is named
`cartographer-<…>`. A skill is verb-object (`cartographer-propose-plan`,
`cartographer-implement-plan`, `cartographer-develop-live`, `cartographer-review-pr`,
`cartographer-audit-clients`); an agent is a role noun that pairs with its skill
(`cartographer-plan-implementer`, `cartographer-live-implementer`,
`cartographer-contract-reviewer`, `cartographer-security-reviewer`,
`cartographer-source-reader`). An agent program is a **client** in names,
descriptions and CONTRIBUTING. A description opens with "Use when …" and the
trigger words, and lists every spawner. Kiro agents carry `tools`, and a read-only
agent has the edit tools removed on Claude Code, OpenCode and Copilot CLI.

**Why.** In a user's catalogue next to other projects' skills, `review-pr` or
`reader` collide or mislead; the old names mixed word orders, did not pair
(`implement-issue` with `plan-implementer`) and hid what an agent reviews. A Kiro
agent without `tools` has none (D300), and read-only held on OpenCode only. The
cost is a one-time rename of every pointer.

**Alternatives rejected.** Prefixing only the agents: the skills collide too.
Renaming the Go identifier `provider` and `docs/harnesses.md`: historical decisions
link that file. Rewriting old decisions and `CHANGELOG.md`: they are records, so
they keep the old names (the one dangling-path check exempts them for the old skill
directories). Renaming the bundled skills here: they reach users, so it is a
separate, breaking plan.

**Consequences.** Read-only is enforced against edits only: those three clients keep a
shell (`go test` needs one), so beyond edits it is instructional. On Kiro it is
instructional alone (`["*"]`, tool names unverified), pinned as a prompt sentence. `TestRepoSubagentsSayTheSameThingOnEveryClient` pins Kiro
`tools` and the read-only restrictions. Kiro's read-only agents list `["*"]` because
the ledger names no read-only tool set; Copilot's list uses the documented
`read`, `search` and `execute` aliases. A new skill or agent takes the prefix and the
description shape.
