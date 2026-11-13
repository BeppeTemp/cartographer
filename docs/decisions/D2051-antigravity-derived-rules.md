---
topic: client-configurator
---

# D2051 — A derived Antigravity binding writes an untracked always-on rules file

**Decision.** When a workspace is bound to Antigravity and its `AGENTS.md` is tracked, the derived binding writes the instructions block into an untracked `.agents/rules/cartographer.md` that opens with `trigger: always_on` frontmatter, excluded through `.git/info/exclude` like `CLAUDE.local.md`. The project-scope hook target is `.agents/hooks.json`. The capability cells of Antigravity are `verified` on Antigravity CLI 1.5.8, with the probe recorded in `docs/harnesses.md`. An existing rules file whose frontmatter lacks `trigger: always_on` is refused per artifact and reported as drift; its frontmatter is never rewritten.

**Why.** Antigravity loads a rule file only with an `always_on` trigger, so a block in a file without it is silently ignored. Writing the tracked `AGENTS.md` would put a personal block in the team's repository. The file is the user's once it exists, so a frontmatter that differs is theirs to fix: the package rewrites only its own managed block.

**Alternatives rejected.** Prepending our header to existing frontmatter: rewrites a hand-curated file. Reporting such a file in sync because it has some frontmatter: the rule never loads and nothing says so. Writing the tracked `AGENTS.md`: pollutes the repository.

**Consequences.** A user-made `.agents/rules/cartographer.md` without `trigger: always_on` makes the sync report a refusal until they add it or remove the file. A new client-written file header has the same check in `blockBodyMatchesHeader` and `writeInstructionsBlockWithHeaderChanged`.
