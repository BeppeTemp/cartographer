---
topic: data-plane
---

# D332 — Artifact findings are accepted with `lint_accept` in `instructions.md`, keyed by path

**Decision.** `instructions.md` may declare `lint_accept`, a mapping from an
artifact path (or a directory prefix ending in `/`) to the checks accepted on
it. Lint drops a matching non-error finding of an artifact check, and reports
a name it cannot accept, or an entry that matches nothing, as
`lint_ignore_invalid` on `instructions.md`. `CheckAcceptability` answers
`artifact` for those checks.

**Why.** Artifact checks were unreachable by `lint_ignore`, so a finding the
operator had judged correct (`skill_git_command` on a skill that pulls a code
repository) stayed forever, and a kb-doctor session could never reach zero.
`instructions.md` already holds the lint-only keys `perimeter` and
`legacy_paths`, and its frontmatter never reaches a client (D61).

**Alternatives rejected.**
- *`lint_ignore` in the skill's or agent's own frontmatter*: that frontmatter
  is shipped to every client, some of which validate the keys they accept;
  a lint setting there is a field no client asked for. A junk file or a script
  under a skill has no frontmatter at all.
- *A new KB-root file*: one more layout entry and provisioning exclusion for a
  list that fits beside `legacy_paths`.

**Consequences.** Junk files and `missing_instructions` stay unacceptable:
the first is deleted, the second has no file to key on. A stale entry is
reported, so an accepted finding that disappears does not leave a silent
exemption behind.
