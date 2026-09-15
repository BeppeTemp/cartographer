---
topic: project-governance
---

# D213 — Every AGENTS.md gets a one-line CLAUDE.md import beside it

**Decision.** Every directory that holds an `AGENTS.md` also holds a `CLAUDE.md`
containing exactly `@AGENTS.md`. That includes the two area files,
`internal/mcpserver/` and `internal/provisioning/`, not only the root. No
directory holds an `AGENTS.override.md`. `internal/repodocs` enforces both
rules on every instruction directory, where it used to check the root file only.

**Why.** Claude Code does not read the name `AGENTS.md` at any depth. It
discovers a nested `CLAUDE.md` and injects it the first time it reads a file in
that directory. So until now the area rules reached Claude only if the agent
followed the root file's instruction to "read it before editing there". For
`internal/provisioning`, a missed rule does not fail a test: the package writes
into other people's home directories. Codex, Kiro and Antigravity already read
the nested `AGENTS.md` themselves, so the import adds nothing for them. It costs
11 bytes per area.

`AGENTS.override.md` is banned because Codex reads it *instead of* the
`AGENTS.md` in the same directory, not in addition to it. One override would
silently hide that directory's rules from Codex only. The chain budget already
counted override files; nothing prevented one from being added.

**Alternatives rejected.**
- *Rely on the root pointer.* Claude only gets the rules if it remembers the
  instruction, which is exactly what automatic loading exists to avoid.
- *A symlink instead of the import.* D207's reason still holds: a Windows
  checkout without Developer Mode turns it into a text file, and the rules
  would silently fail to load.
- *`.claude/rules/*.md` with `paths:` globs.* It is more precise, but it is a
  second place for the same rules, and no repository measured uses it yet.

**Consequences.** Adding an area `AGENTS.md` means adding its `CLAUDE.md` in the
same change. `make test` names the missing one. A Codex override now fails the
build rather than shadowing the rules.
