---
topic: project-governance
---

# D2323 — The second client's agent definitions are generated from the Claude ones

**Decision.** The role bodies stay the files of `.claude/agents/`; the second client's
definitions, `.kiro/agents/cartographer-<role>.json`, are generated from them
(`make kiro-agents`) and hold a pointer to the body, never a copy, so a body edit needs no
regeneration and a change of name, description, model or tools does. A test holds the
parity. Everything under `.kiro/` but those files is git-ignored, so another tool writing
there keeps a clean tree.

**Why.** The last release was built with several lanes on a second client whose definitions
lived outside the repository, so the way they were written was known to one machine only.
D992 removed a four-client tree for good reasons: three surfaces never exercised, a copy of
every body held by a parity test, symlinks that a Windows checkout turns into text files.
A generator and one test keep the cost at what a pointer needs, with none of those.

**Alternatives rejected.** Hand-kept copies: the parity cost D992 removed. Symlinks into
`.claude/agents/`: the Windows trap. Definitions outside the repository: the problem this
answers.

**Consequences.** One body per role; no symlink in the tree; the root `AGENTS.md` does not
grow; a reviewer has no write tool, no `code` tool (it can rewrite) and no `fs_write` rule,
and no role is pre-approved for the shell: the generated rules never grant more than the
`.claude` source has. The `permissions.rules` shape and the `resources` pointer are what the
maintainer's probe checks on the client (one read-only role spawned, its log showing the
pinned model). Cartographer's own client may provision agents into the same folder: its prune
removes only what its lockfile owns, and these files are in no lockfile; the `cartographer-`
prefix is the repository's.
