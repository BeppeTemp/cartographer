---
topic: project-governance
supersedes: D992
---

# D2323 — The repository is developed with two clients, the second one's definitions generated

**Decision.** The repository is developed with two clients, Claude Code and Kiro CLI.
`AGENTS.md` stays the canonical instruction file with its one-line `CLAUDE.md` imports (D213);
the skills are real directories in `.claude/skills/` and the role bodies the files of
`.claude/agents/`; the second client's definitions,
`.kiro/agents/cartographer-<role>.json`, are generated from them (`make kiro-agents`) and
hold a pointer to the body, never a copy, so a body edit needs no regeneration and a
change of name, description, model or tools does. A test holds the parity. Everything
under `.kiro/` but those files is git-ignored, so another tool writing there keeps a clean tree.
`CONTRIBUTING.md` §Working with an agent client carries one table of what differs between
the two clients, and a test holds its columns to the declared clients, each with a test of
its definitions.

**Kept from D992.** One body per role; the skills and the agents of `.claude/` are real
directories; no symlink in the tree; the required `plan-gate` check depends on no skill path
(`scripts/pr-triage.sh`); the product's support for every client (the configurator, the provisioning matrix,
`docs/harnesses.md`) is untouched and is not described by this decision.

**Why.** The last release was built with several lanes on a second client whose definitions
lived outside the repository, so the way they were written was known to one machine only.
D992 removed a four-client tree for good reasons: three surfaces never exercised, a copy of
every body held by a parity test, symlinks that a Windows checkout turns into text files.
Those reasons stay; its conclusion (one client) does not fit what is used. Two clients, the
two used, keep the cost at what a generator and one test carry.

**Alternatives rejected.** More clients: untested paths again; a third client keeps today's
sentence (read `AGENTS.md`, follow a skill as a plain procedure). Hand-kept copies: the parity
cost D992 removed. Symlinks into `.claude/agents/`: the Windows trap. Definitions outside
the repository: the problem this answers.

**Consequences.** The root `AGENTS.md` does not grow; a reviewer has no write tool, no `code`
tool (it can rewrite) and no `fs_write` rule, and no role is pre-approved for the shell: the
generated rules never grant more than the `.claude` source has. The `permissions.rules` shape
and the `resources` pointer are what the maintainer's probe checks on the client (one
read-only role spawned, its log showing the pinned model). Cartographer's own client may
provision agents into the same folder: its prune removes only what its lockfile owns, and
these files are in no lockfile; the `cartographer-` prefix is the repository's.
