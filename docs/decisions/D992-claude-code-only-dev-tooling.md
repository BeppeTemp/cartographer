---
topic: project-governance
supersedes: D884
superseded_by: D2323
amends: D203, D327
---

# D992 — This repository is developed with Claude Code only

> **Superseded by [D2323](D2323-two-dev-clients.md)**: the repository is developed with two clients, Claude Code and Kiro CLI, the second one's agent definitions generated from the first one's; what D992 keeps is listed there.

**Decision.** The repository's development tooling targets Claude Code alone:
skills are real directories in `.claude/skills/`, subagents are the five files in
`.claude/agents/`, and `AGENTS.md` stays the canonical instruction file with a
one-line `CLAUDE.md` import beside it (D213). The Kiro, OpenCode and Copilot CLI
copies (`.kiro/`, `.opencode/`, `.github/agents/`), the `.agents/skills/` tree
and every symlink bridge are deleted. A contributor using another agent points it
at `AGENTS.md` and reads a skill or an agent body as a plain procedure.
`triage.sh` moves out of the skill tree to `scripts/pr-triage.sh`, so the required
`plan-gate` check no longer depends on a skill path (the #988 failure), and
`TestPlanGateWorkflowNeverRunsPRCode` pins that. Product support for every client
(the configurator, the provisioning matrix, `docs/harnesses.md`, the
`cartographer-audit-clients` skill) is untouched.

**Why.** Only Claude Code is used here. The other three surfaces were never
exercised, their tool lists were partly unverified (D957), every change to an
agent or a skill paid a four-way cost held by a parity test, and a Windows
checkout without Developer Mode silently turned the symlinks into text files.

**Alternatives rejected.** Keeping the four surfaces: the cost without the use.
Keeping `.agents/skills/` as the canonical tree with only a `.claude` bridge: the
symlink trap stays for one client and nothing reads the canonical path.

**Consequences.** `TestRepoSubagentsSayTheSameThingOnEveryClient` and the symlink
gate are replaced by `TestClaudeSubagentsPinAModelAndReadOnlyOnesCannotEdit`.
Skills stay valid agentskills.io files (`TestSkillFrontmatterMatchesTheStrictestClient`).
A client other than Claude Code gets no tested path in this repository.
Supersedes D884 and the multi-client parts of D327 and D203.
