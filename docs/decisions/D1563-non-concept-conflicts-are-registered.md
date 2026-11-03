---
topic: data-plane
---

# D1563 — A rebase conflict on any non-concept file is registered, as kind artifact

**Decision.** A conflicting file that is neither a concept nor a reserved file is registered in the conflict registry with `kind: artifact` and its git path as `concept_id`, whatever else conflicted in the same rebase. `git_conflict_resolve` takes `ours` or `theirs` for it (no `edit`, no `union`: the file may be binary or not a concept), and `kb.FinalizeConflicts` settles it with the concepts. The write's error message promises a tool only for what was registered.

**Why.** Conflicts were registered only for concept and reserved paths. A conflict on `skills/x/SKILL.md`, an expanded concept's image, a template, `instructions.md`, `settings.yaml` or `paths.yaml` was registered nowhere: the write failed with "registered on 0 concept(s)", `conflicts_list` said "No open conflicts.", and every later write redid the rebase and failed again, with no tool-level way out. Registering alongside concepts matters too: `FinalizeConflicts` refuses a merge with unregistered conflicting files.

**Alternatives rejected.**
- Auto-resolve to `theirs` and keep the local version in a backup commit or branch: silently drops a local edit, and needs a second place to look for it.
- Allow `edit` with a body: `Body` is a string validated as a concept (frontmatter required), wrong for a binary asset or a YAML settings file.

**Consequences.** `conflicts_list` can show `kind: artifact`; clients that switch on `kind` must tolerate it. `ours`/`theirs` read the file from the local/remote SHA, so a file deleted on one side cannot take that side's strategy.
